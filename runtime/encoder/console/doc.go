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

// Package console implements a human-oriented, single-line encoder
// on top of go.uber.org/zap's ConsoleEncoder.
//
// Design:
//   - Public API stays vendor-neutral (dirpx.dev/dxlog/apis/*).
//   - This package is a runtime-only adapter: it maps apis types to zapcore.Entry/Field.
//   - Deterministic field order (sorted keys) to keep output stable across runs.
//
// Notes:
//   - Newline framing is controlled by encoder.Options.AppendNewline.
//   - Pretty option is a no-op for console; the console encoder is inherently readable.
//   - This package does not close the provided io.Writer.
package console
