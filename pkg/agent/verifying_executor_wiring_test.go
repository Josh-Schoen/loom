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

package agent

import (
	"testing"

	"github.com/teradata-labs/loom/pkg/project/oracle"
)

func TestNewAgentWiresVerificationOracle(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{name: "minimal agent"},
		{name: "named agent", opts: []Option{WithName("verifier-wiring")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAgent(nil, nil, tt.opts...)
			if a.executor == nil {
				t.Fatal("executor not constructed")
			}
			if a.verifier == nil {
				t.Fatal("verifier not constructed")
			}
			if _, ok := a.toolExecutor().(*oracle.VerifyingExecutor); !ok {
				t.Fatalf("toolExecutor() = %T, want *oracle.VerifyingExecutor", a.toolExecutor())
			}
		})
	}
}

func TestToolExecutorFallsBackToExecutor(t *testing.T) {
	a := NewAgent(nil, nil)
	a.verifier = nil
	if got := a.toolExecutor(); got != oracle.ToolExecutor(a.executor) {
		t.Fatalf("toolExecutor() = %T, want the bare executor", got)
	}
}
