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
package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/teradata-labs/loom/pkg/storage/postgres"
)

// turnIdentityContext is the seam every background-driven turn's context is
// rooted in: the tenant identity MUST survive (the postgres HITL store refuses
// any operation without it — a held call would be denied in microseconds with
// no card) and the caller's cancellation and deadline must NOT (the worker
// outlives the request).
func TestTurnIdentityContext(t *testing.T) {
	t.Run("carries the tenant identity", func(t *testing.T) {
		ctx := postgres.ContextWithUserID(context.Background(), "tenant-42")
		got := turnIdentityContext(ctx)
		require.Equal(t, "tenant-42", postgres.UserIDFromContext(got),
			"rewriting the body to context.Background() must fail here")
	})

	t.Run("drops the caller's cancellation and deadline", func(t *testing.T) {
		parent, cancel := context.WithCancel(postgres.ContextWithUserID(context.Background(), "tenant-42"))
		cancel()
		got := turnIdentityContext(parent)
		require.NoError(t, got.Err(), "the worker context must outlive the request that spawned it")
		_, hasDeadline := got.Deadline()
		require.False(t, hasDeadline)
		require.Equal(t, "tenant-42", postgres.UserIDFromContext(got))
	})

	t.Run("empty identity stays empty", func(t *testing.T) {
		got := turnIdentityContext(context.Background())
		require.Empty(t, postgres.UserIDFromContext(got),
			"a SQLite deployment's identity-less context behaves like context.Background()")
	})
}
