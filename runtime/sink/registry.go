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

package sink

import (
	"context"

	asink "dirpx.dev/dlog/apis/sink"
	"dirpx.dev/dlog/runtime/registry"
)

// Registry is a global sink registry, case-insensitive for convenience.
var Registry = registry.New[asink.Sink, asink.Specification](registry.WithCaseFoldLower())

// Register registers a sink builder under (kind, name).
// Typical usage from package init(): Register("sink", "stdout", build)
func Register(kind, name string, b registry.Builder[asink.Sink, asink.Specification]) {
	registry.MustRegister(Registry, registry.Key{Kind: kind, Name: name}, b)
}

// Build constructs a sink instance from the registered builder.
func Build(ctx context.Context, kind, name string, spec asink.Specification) (asink.Sink, error) {
	return Registry.Build(ctx, registry.Key{Kind: kind, Name: name}, spec)
}

// Seal prevents further registrations (optional, once all init() done).
func Seal() { Registry.Seal() }
