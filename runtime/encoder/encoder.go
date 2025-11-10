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

import (
	"io"

	"dirpx.dev/dxlog/apis/record"
)

// Encoder converts a Record into bytes and writes them to an io.Writer.
// Implementations must be safe for concurrent use by multiple goroutines,
// or explicitly document the opposite.
type Encoder interface {
	// Encode serializes the record and writes it to w.
	// Implementations must not close w.
	Encode(r *record.Record, w io.Writer) error

	// ContentType returns the MIME content type of the encoded output.
	// Example: "application/json".
	ContentType() string

	// Name returns a short stable name for this encoder ("json", "text", ...).
	Name() string
}
