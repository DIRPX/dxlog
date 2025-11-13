/*
   Copyright 2025 The DIRPX Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package policy

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	asink "dirpx.dev/dlog/apis/sink"
	spolicy "dirpx.dev/dlog/apis/sink/policy"
)

var (
	// ErrQueueFull is returned when the queue is full and OverflowDrop is used.
	ErrQueueFull = errors.New("sink/policy: async queue full")
	// ErrBatchClosed is returned when Write/Flush is called after Close.
	ErrBatchClosed = errors.New("sink/policy: batch sink closed")
)

// BatchOptions configures the runtime batching behavior around a sink.
//
// It is a runtime counterpart of apis/sink/policy.Batch + Backpressure:
//
//   - QueueSize  → размер внутренней очереди (channel buffer)
//   - Batch      → MaxEntries + Interval (когда триггерить flush)
//   - Backpressure → Block/Drop/Shed, что делать при переполнении очереди
type BatchOptions struct {
	// QueueSize controls the size of the in-memory queue (channel buffer).
	// If <= 0, a default of 1024 is used.
	QueueSize int

	// Batch describes when the batch should be flushed, as declared in apis.
	//   - MaxEntries > 0: flush when this many entries are accumulated.
	//   - Interval  > 0: flush periodically even if MaxEntries not reached.
	// If both are zero, flush happens only on Close.
	Batch spolicy.Batch

	// Backpressure defines what happens when the internal queue is full.
	//   - BackpressureBlock: block until there is room (or ctx is cancelled)
	//   - BackpressureDrop:  drop the entry and return ErrQueueFull
	//   - BackpressureShed:  same as Drop in MVP; may grow into more aggressive
	//                        shedding strategy in the future.
	Backpressure spolicy.Backpressure

	// Name overrides the sink name. If empty, the wrapper reports
	// its name as "batch(<inner.Name()>)".
	Name string
}

// batchSink is a sink wrapper that enqueues encoded entries into a bounded
// channel and writes them to the underlying sink from a dedicated worker goroutine.
//
// Semantics:
//   - Write: enqueues the entry and returns when it is accepted by the queue,
//     not when it is actually persisted by the underlying sink.
//   - Flush: delegates to the underlying sink's Flush; does NOT guarantee that
//     the queue is empty. Use Close to guarantee delivery.
//   - Close: stops accepting new entries, drains the queue according to
//     Batch.MaxEntries / Batch.Interval, then closes the underlying sink.
type batchSink struct {
	next asink.Sink
	opt  BatchOptions

	queue chan []byte
	stop  chan struct{}
	done  chan struct{}

	closed atomic.Bool
}

// Compile-time check: *batchSink implements asink.Sink.
var _ asink.Sink = (*batchSink)(nil)

// WithBatch wraps a sink with an asynchronous batching layer that follows
// the declarative policy from BatchOptions (which itself is based on
// apis/sink/policy.Batch and Backpressure).
func WithBatch(next asink.Sink, opt BatchOptions) asink.Sink {
	if opt.QueueSize <= 0 {
		opt.QueueSize = 1024
	}
	// Default backpressure: block (0 == BackpressureBlock).
	if opt.Backpressure != spolicy.BackpressureBlock &&
		opt.Backpressure != spolicy.BackpressureDrop &&
		opt.Backpressure != spolicy.BackpressureShed {
		opt.Backpressure = spolicy.BackpressureBlock
	}

	s := &batchSink{
		next:  next,
		opt:   opt,
		queue: make(chan []byte, opt.QueueSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go s.run()
	return s
}

// Name returns the human-friendly name of the sink.
func (s *batchSink) Name() string {
	if s.opt.Name != "" {
		return s.opt.Name
	}
	return "batch(" + s.next.Name() + ")"
}

// Write enqueues a single encoded entry into the batch queue.
//
// Notes:
//   - The entry slice is copied before enqueuing to avoid aliasing issues
//     with caller-managed buffers.
//   - When the queue is full:
//   - BackpressureBlock: blocks until there is space or ctx is cancelled.
//   - BackpressureDrop / BackpressureShed: drops entry and returns ErrQueueFull.
//   - After Close, Write returns ErrBatchClosed.
func (s *batchSink) Write(ctx context.Context, entry []byte) error {
	if s.closed.Load() {
		return ErrBatchClosed
	}

	// Copy entry to decouple from caller buffer lifetime.
	buf := make([]byte, len(entry))
	copy(buf, entry)

	// Fast path: try non-blocking enqueue.
	select {
	case s.queue <- buf:
		return nil
	default:
	}

	switch s.opt.Backpressure {
	case spolicy.BackpressureDrop, spolicy.BackpressureShed:
		// MVP: Shed behaves the same as Drop. In the future this could
		// decide to shed more aggressively (e.g. discard older entries).
		return ErrQueueFull
	case spolicy.BackpressureBlock:
		// Block until room is available or context is cancelled.
		select {
		case s.queue <- buf:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	default:
		// Unknown mode: fall back to safe blocking behavior.
		select {
		case s.queue <- buf:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Flush delegates to the underlying sink's Flush. It does not guarantee that
// the async queue is empty; entries that are still buffered will be flushed
// later by the worker.
//
// After Close, Flush returns ErrBatchClosed.
func (s *batchSink) Flush(ctx context.Context) error {
	if s.closed.Load() {
		return ErrBatchClosed
	}
	return s.next.Flush(ctx)
}

// Close stops accepting new entries, drains the queue according to the
// batch policy (MaxEntries / Interval), and then closes the underlying sink.
//
// Close is idempotent; subsequent calls are no-ops.
//
// The method blocks until the worker has finished draining the queue or until
// ctx is cancelled. If ctx is cancelled while draining, the underlying sink
// may still receive some entries; Close will then return ctx.Err().
func (s *batchSink) Close(ctx context.Context) error {
	if s.closed.Swap(true) {
		// Already closed.
		return nil
	}

	// Signal worker to stop and drain remaining entries.
	close(s.stop)

	// Wait for the worker to finish, but respect context cancellation.
	select {
	case <-s.done:
		// Worker has drained and exited.
	case <-ctx.Done():
		return ctx.Err()
	}

	// Finally, close the underlying sink.
	return s.next.Close(ctx)
}

// run is the worker loop that drains the queue and writes to the underlying sink.
func (s *batchSink) run() {
	defer close(s.done)

	var (
		batch      [][]byte
		maxEntries = s.opt.Batch.MaxEntries
		ticker     *time.Ticker
		flushC     <-chan time.Time
	)

	if s.opt.Batch.Interval > 0 {
		ticker = time.NewTicker(s.opt.Batch.Interval)
		flushC = ticker.C
		defer ticker.Stop()
	}

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Best-effort: ignore underlying errors here. If stronger guarantees
		// are required, compose this wrapper with a Retry policy.
		for _, e := range batch {
			_ = s.next.Write(context.Background(), e)
		}
		_ = s.next.Flush(context.Background())
		batch = batch[:0]
	}

	for {
		select {
		case e := <-s.queue:
			// Channel is never closed externally; we never send nil entries.
			if e == nil {
				continue
			}
			batch = append(batch, e)
			if maxEntries > 0 && len(batch) >= maxEntries {
				flush()
			}

		case <-flushC:
			flush()
		case <-s.stop:
			// Drain remaining entries before exiting.
			for {
				select {
				case e := <-s.queue:
					if e == nil {
						continue
					}
					batch = append(batch, e)
					if maxEntries > 0 && len(batch) >= maxEntries {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}
