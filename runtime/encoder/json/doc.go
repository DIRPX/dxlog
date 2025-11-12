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

// Package json implements a JSON (NDJSON by default) encoder on top of zap's
// JSON encoder.
//
// Defaults:
//   - NDJSON framing: one JSON object per line (AppendNewline defaults to true).
//   - Deterministic field order (sorted keys).
//
// Options:
//   - Pretty: no-op (zap JSON does not pretty-print). Use console encoder for human logs.
//   - EscapeHTML: not exposed by zap; ignored.
//   - AppendNewline: when false, the trailing newline is removed.
//
// Rationale:
//   - Public API remains zap-agnostic. Mapping to zap is kept inside runtime.
//
// Caveat:
//   - If you need strict control over JSON escaping/indentation, consider the
//     stdlib-based encoder as an alternative implementation behind the same interface.
package json
