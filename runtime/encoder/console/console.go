package console

import (
	"io"

	"dirpx.dev/dlog/apis/record"
	"dirpx.dev/dlog/runtime/encoder"
	"dirpx.dev/dlog/runtime/encoder/internalzap"
	"go.uber.org/zap/zapcore"
)

// Compile-time check: *Encoder implements encoder.Encoder.
var _ encoder.Encoder = (*Encoder)(nil)

const (
	consoleName        = "console(zap)"
	consoleContentType = "text/plain; charset=utf-8"
)

// Encoder adapts zapcore.ConsoleEncoder to dlog's encoder.Encoder.
//
// Concurrency:
//
//	A zapcore.Encoder instance is not safe for concurrent use. This type
//	stores a "prototype" encoder and calls Clone() per Encode invocation,
//	making each encode operation independent and safe across goroutines.
//
// Line framing:
//
//	Line endings are normalized according to encoder.Options.AppendNewline
//	via internalzap.NormalizeLineEnding (default: "\n" to mirror NDJSON).
type Encoder struct {
	base       zapcore.Encoder // prototype; Clone() per call
	lineEnding string          // "\n" or ""
}

// New constructs a console (text) encoder backed by zap's ConsoleEncoder.
//
// Options behavior:
//   - Pretty: no-op for console; output is human-friendly by design.
//   - EscapeHTML: not applicable for console; ignored.
//   - AppendNewline: when unset or true → ensures a trailing '\n';
//     when false → strips the trailing '\n'.
func New(opt encoder.Options) *Encoder {
	cfg := internalzap.DefaultEncoderConfig()
	return &Encoder{
		base:       zapcore.NewConsoleEncoder(cfg),
		lineEnding: internalzap.PickLineEnding(opt.AppendNewline), // default: "\n"
	}
}

// Name returns a short, stable identifier for this encoder implementation.
func (e *Encoder) Name() string { return consoleName }

// ContentType returns the MIME type for console output.
func (e *Encoder) ContentType() string { return consoleContentType }

// Encode maps record.Record into zapcore.Entry + []zapcore.Field and encodes it
// using a cloned zap encoder. The writer is never closed.
//
// Mapping rules (vendor-neutral):
//   - Timestamp: taken via a Timestamp() method when present; otherwise zero.
//   - Level:     prefers apis/level.Level; falls back to string Level(); maps to zapcore.Level.
//   - Message:   taken via Message() method when present; otherwise empty.
//   - Fields:    taken via Fields() map[string]any when present; keys sorted for determinism.
//
// Line ending:
//
//	Zap encoders apply their own default line endings. We normalize the final
//	bytes with internalzap.NormalizeLineEnding so AppendNewline is honored
//	regardless of zap's defaults.
func (e *Encoder) Encode(r *record.Record, w io.Writer) error {
	// Clone per call for concurrency-safety.
	zenc := e.base.Clone()

	entry := zapcore.Entry{
		Time:    internalzap.ExtractTimestamp(any(r)),
		Level:   internalzap.ExtractZapLevel(any(r)),
		Message: internalzap.ExtractMessage(any(r)),
		// LoggerName/Caller/Stacktrace are intentionally left empty here.
	}
	fields := internalzap.ToZapFields(internalzap.ExtractFields(any(r)))

	buf, err := zenc.EncodeEntry(entry, fields)
	if err != nil {
		return err
	}
	// Normalize line ending to honor AppendNewline semantics.
	out := internalzap.NormalizeLineEnding(buf.Bytes(), e.lineEnding)

	// Write before freeing the zap buffer (EncodeEntry returns a pooled buffer).
	_, werr := w.Write(out)
	buf.Free()
	return werr
}
