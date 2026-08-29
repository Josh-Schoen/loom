// Copyright 2026 Teradata
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shuttle

import (
	"context"
	"strings"
	"testing"

	"github.com/teradata-labs/loom/pkg/storage"
)

// Threshold semantics: -1 inlines everything, 0 references everything,
// N>0 references only results larger than N bytes. The -1 case silently
// inverted into reference-everything before the guard (agents never saw
// small results inline — the greetings-instead-of-answers bug).
func TestHandleLargeResultThresholdSemantics(t *testing.T) {
	mem := storage.NewSharedMemoryStore(&storage.Config{})
	small := strings.Repeat("x", 100)
	large := strings.Repeat("y", 20000)

	cases := []struct {
		name      string
		threshold int64
		data      string
		wantRef   bool
	}{
		{"inline everything keeps small inline", -1, small, false},
		{"inline everything keeps large inline", -1, large, false},
		{"always reference stores small", 0, small, true},
		{"threshold keeps small inline", 16384, small, false},
		{"threshold stores large", 16384, large, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewExecutor(nil)
			e.SetSharedMemory(mem, tc.threshold)
			if tc.threshold < 0 {
				// SetSharedMemory ignores negatives; the constructor default
				// (-1) is the inline-everything path.
				e = NewExecutor(nil)
				e.sharedMemory = mem
			}
			res := &Result{Success: true, Data: tc.data}
			if err := e.handleLargeResult(context.Background(), res); err != nil {
				t.Fatalf("handleLargeResult: %v", err)
			}
			gotRef := res.DataReference != nil
			if gotRef != tc.wantRef {
				t.Errorf("threshold %d, %d bytes: ref = %v, want %v",
					tc.threshold, len(tc.data), gotRef, tc.wantRef)
			}
		})
	}
}
