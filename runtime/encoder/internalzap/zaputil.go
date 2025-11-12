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

// Package internalzap hosts small utilities for adapting dlog's
// vendor-neutral runtime to zap encoders. It provides a compact,
// deterministic mapping from dlog record concepts to zapcore types,
// plus shared configuration helpers used by console and json encoders.
package internalzap

import (
	"sort"
	"strings"
	"time"

	alevel "dirpx.dev/dlog/apis/level"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// -----------------------------------------------------------------------------
// Encoder configuration & options
// -----------------------------------------------------------------------------

// DefaultEncoderConfig returns a minimal, stable zap EncoderConfig shared by
// both console and JSON adapters. We deliberately leave caller/name/stack
// keys empty—dlog controls those concerns at higher layers.
func DefaultEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "",
		CallerKey:      "",
		MessageKey:     "msg",
		StacktraceKey:  "",
		LineEnding:     "\n", // final framing normalized by NormalizeLineEnding
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}

// PickLineEnding converts an optional boolean into a concrete line ending.
// Semantics:
//   - nil or true  => "\n" (NDJSON-style framing)
//   - false        => ""   (no trailing newline)
func PickLineEnding(p *bool) string {
	if p == nil || *p {
		return "\n"
	}
	return ""
}

// NormalizeLineEnding enforces the desired trailing newline policy on the
// encoded byte slice, independent of zap's internal defaults.
//
// Behavior:
//   - ending == "\n": ensure a single trailing '\n' (idempotent)
//   - ending == "":   ensure no trailing '\n'
func NormalizeLineEnding(b []byte, ending string) []byte {
	if ending == "\n" {
		if len(b) > 0 && b[len(b)-1] == '\n' {
			return b
		}
		out := make([]byte, 0, len(b)+1)
		out = append(out, b...)
		return append(out, '\n')
	}
	// ending == ""
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return b[:len(b)-1]
	}
	return b
}

// -----------------------------------------------------------------------------
// Extraction from record.Record (vendor-neutral)
// -----------------------------------------------------------------------------

// The following tiny interfaces let us discover record properties without
// importing or depending on concrete dlog record implementations.

type hasTS interface{ Timestamp() time.Time }

// Prefer apis/level.Level, but accept a string-based Level() as a fallback.
type hasAPIsLevel interface{ Level() alevel.Level }
type hasStringLevel interface{ Level() string }

type hasMsg interface{ Message() string }
type hasFields interface{ Fields() map[string]any }

// ExtractTimestamp returns the record timestamp when available, or zero time.
func ExtractTimestamp(v any) time.Time {
	if h, ok := v.(hasTS); ok {
		return h.Timestamp()
	}
	return time.Time{}
}

// ExtractZapLevel reads dlog's level (typed or string) and maps it to a
// zapcore.Level. Missing/unknown levels default to Info.
func ExtractZapLevel(v any) zapcore.Level {
	if h, ok := v.(hasAPIsLevel); ok {
		return MapAPIsLevel(h.Level())
	}
	if h, ok := v.(hasStringLevel); ok {
		return MapStringLevel(h.Level())
	}
	return zapcore.InfoLevel
}

// ExtractMessage returns the message string when available, otherwise empty.
func ExtractMessage(v any) string {
	if h, ok := v.(hasMsg); ok {
		return h.Message()
	}
	return ""
}

// ExtractFields returns the record fields map when available, otherwise nil.
func ExtractFields(v any) map[string]any {
	if h, ok := v.(hasFields); ok {
		return h.Fields()
	}
	return nil
}

// -----------------------------------------------------------------------------
// Level mapping (apis -> zap)
// -----------------------------------------------------------------------------

// MapAPIsLevel converts dlog's typed level to a zap level. It relies on
// a canonical String() representation of alevel.Level. If you later switch
// to numeric levels, this function can branch on those without changing callers.
func MapAPIsLevel(l alevel.Level) zapcore.Level {
	return MapStringLevel(strings.ToLower(l.String()))
}

// MapStringLevel converts common string level names to zapcore.Level.
// Unrecognized values fall back to Info.
func MapStringLevel(s string) zapcore.Level {
	switch strings.ToLower(s) {
	case "trace", "debug":
		return zapcore.DebugLevel
	case "info", "":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "dpanic":
		return zapcore.DPanicLevel
	case "panic":
		return zapcore.PanicLevel
	case "fatal":
		return zapcore.FatalLevel
	default:
		return zapcore.InfoLevel
	}
}

// -----------------------------------------------------------------------------
// Fields conversion (deterministic order)
// -----------------------------------------------------------------------------

// ToZapFields converts a generic map into a sorted slice of zap fields for
// stable, deterministic output. Keys are sorted lexicographically.
func ToZapFields(m map[string]any) []zapcore.Field {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fs := make([]zapcore.Field, 0, len(keys))
	for _, k := range keys {
		fs = append(fs, zap.Any(k, m[k])) // zap.Any returns zapcore.Field
	}
	return fs
}
