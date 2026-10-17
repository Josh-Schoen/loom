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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/teradata-labs/loom/pkg/storage/postgres"
)

// postSSE posts a StreamWeave request to the SSE handler with headers.
func postSSE(t *testing.T, h *HTTPServer, headers map[string]string) *http.Response {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(h.handleStreamWeaveSSE))
	t.Cleanup(ts.Close)
	req, err := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"query":"hi"}`))
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// An interceptor that rejects the call (e.g. a missing bearer token when auth
// is enabled) yields a plain HTTP error and StreamWeave never runs. The
// server has no gRPC service attached, so running it would fail the test.
func TestStreamWeaveSSE_InterceptorRejectionIsHTTPError(t *testing.T) {
	h := NewHTTPServer(nil, "", "", zaptest.NewLogger(t))
	h.SetStreamInterceptors(func(any, grpc.ServerStream, *grpc.StreamServerInfo, grpc.StreamHandler) error {
		return status.Error(codes.Unauthenticated, "authorization bearer token required")
	})

	resp := postSSE(t, h, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.NotEqual(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

// With the gRPC user-ID interceptor installed, an SSE stream is attributed the
// way a gateway call is: the default user when no identity is sent, or the
// user forwarded in a Grpc-Metadata-X-User-Id header. A second interceptor
// records the identity StreamWeave would see and stops the call there.
func TestStreamWeaveSSE_IdentityMatchesGateway(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"default identity", nil, "default-user"},
		{"forwarded user", map[string]string{"Grpc-Metadata-X-User-Id": "alice"}, "alice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := NewHTTPServer(nil, "", "", zaptest.NewLogger(t))
			h.SetStreamInterceptors(
				UserIDStreamInterceptor(UserIDConfig{DefaultUserID: "default-user"}),
				func(_ any, ss grpc.ServerStream, info *grpc.StreamServerInfo, _ grpc.StreamHandler) error {
					assert.Equal(t, streamWeaveFullMethod, info.FullMethod)
					got = postgres.UserIDFromContext(ss.Context())
					return status.Error(codes.PermissionDenied, "stop before the agent runs")
				},
			)

			resp := postSSE(t, h, tt.headers)
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The chain runs interceptors first-outermost, like grpc.ChainStreamInterceptor,
// and is nil when there are none (embedders and tests that configure none get
// the previous behavior).
func TestChainStreamInterceptors_Order(t *testing.T) {
	var order []string
	mk := func(name string) grpc.StreamServerInterceptor {
		return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			order = append(order, name)
			return handler(srv, ss)
		}
	}
	assert.Nil(t, chainStreamInterceptors(nil))

	chain := chainStreamInterceptors([]grpc.StreamServerInterceptor{mk("first"), mk("second")})
	err := chain(nil, &sseStreamWrapper{ctx: context.Background()}, &grpc.StreamServerInfo{},
		func(any, grpc.ServerStream) error {
			order = append(order, "handler")
			return nil
		})
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second", "handler"}, order)
}
