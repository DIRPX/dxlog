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

package encoder

// Options controls common encoder behavior.
// Fields are intentionally minimal and implementation-agnostic.
type Options struct {
	// Pretty asks the encoder (if supported) to use a human-friendly layout.
	// For JSON this enables SetIndent("", "  ").
	Pretty bool

	// EscapeHTML controls whether HTML characters are escaped (if supported).
	// For JSON the default in Go's stdlib is true; in dlog we default to false
	// because log consumers rarely need HTML-escaped JSON. A nil value means
	// "use encoder default".
	EscapeHTML *bool

	// AppendNewline requests a trailing '\n' (if supported).
	// For NDJSON this should default to true. A nil value means "use encoder default".
	AppendNewline *bool
}

// WithDefaults applies encoder-default values while preserving explicit choices.
func (o Options) WithDefaults(def Defaults) Options {
	out := o

	if out.EscapeHTML == nil {
		v := def.EscapeHTML
		out.EscapeHTML = &v
	}
	if out.AppendNewline == nil {
		v := def.AppendNewline
		out.AppendNewline = &v
	}
	// Pretty has a zero-value default (false) which is fine here.
	return out
}

// Defaults defines per-encoder baseline defaults.
type Defaults struct {
	EscapeHTML    bool
	AppendNewline bool
}
