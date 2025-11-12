package json

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
	jsonName        = "json(zap)"
	jsonContentType = "application/json"
)

// Encoder adapts zapcore.JSONEncoder to dlog's encoder.Encoder.
//
// Concurrency:
//
//	zapcore.Encoder is not safe for concurrent use. This type stores a
//	"prototype" encoder and calls Clone() on every Encode, making concurrent
//	calls safe.
//
// Line framing:
//
//	Line endings are normalized according to encoder.Options.AppendNewline via
//	internalzap.NormalizeLineEnding (default: "\n", i.e. NDJSON).
type Encoder struct {
	base       zapcore.Encoder // prototype; Clone() per call
	lineEnding string          // "\n" or ""
}

// New constructs a JSON encoder backed by zap's JSON encoder.
//
// Options behavior:
//   - Pretty: no-op (zap's JSON encoder doesn't pretty-print).
//   - EscapeHTML: not exposed by zap; ignored.
//   - AppendNewline: when unset or true → ensure a trailing '\n';
//     when false       → strip the trailing '\n'.
func New(opt encoder.Options) *Encoder {
	cfg := internalzap.DefaultEncoderConfig()
	return &Encoder{
		base:       zapcore.NewJSONEncoder(cfg),
		lineEnding: internalzap.PickLineEnding(opt.AppendNewline), // default: "\n"
	}
}

// Name returns a short, stable identifier for this encoder.
func (e *Encoder) Name() string { return jsonName }

// ContentType returns the MIME type for JSON output.
func (e *Encoder) ContentType() string { return jsonContentType }

// Encode maps record.Record into zapcore.Entry + []zapcore.Field and encodes it
// using a cloned zap encoder. The writer is never closed.
//
// Mapping rules (vendor-neutral):
//   - Timestamp: via Timestamp() when present; otherwise zero.
//   - Level:     prefers apis/level.Level; falls back to string Level(); mapped to zapcore.Level.
//   - Message:   via Message() when present; otherwise empty.
//   - Fields:    via Fields() map[string]any when present; keys sorted for determinism.
//
// Line ending:
//
//	Zap encoders have their own default line ending. We normalize the final
//	bytes with internalzap.NormalizeLineEnding so AppendNewline semantics are
//	honored regardless of zap internals.
func (e *Encoder) Encode(r *record.Record, w io.Writer) error {
	// Clone per call for concurrency-safety.
	zenc := e.base.Clone()

	entry := zapcore.Entry{
		Time:    internalzap.ExtractTimestamp(any(r)),
		Level:   internalzap.ExtractZapLevel(any(r)),
		Message: internalzap.ExtractMessage(any(r)),
		// LoggerName/Caller/Stacktrace are intentionally omitted at encoder level.
	}
	fields := internalzap.ToZapFields(internalzap.ExtractFields(any(r)))

	buf, err := zenc.EncodeEntry(entry, fields)
	if err != nil {
		return err
	}

	// Normalize line ending according to AppendNewline.
	out := internalzap.NormalizeLineEnding(buf.Bytes(), e.lineEnding)

	// Write before freeing the zap buffer.
	_, werr := w.Write(out)
	buf.Free()
	return werr
}
