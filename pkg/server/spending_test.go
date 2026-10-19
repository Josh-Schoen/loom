// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
	"github.com/teradata-labs/loom/pkg/llm/spending"
)

// Over the monthly limit, a new turn is refused before any agent runs, with
// RESOURCE_EXHAUSTED and the limit in words.
func TestWeaveRefusedOverSpendingLimit(t *testing.T) {
	ledger, err := spending.Open(filepath.Join(t.TempDir(), "spending.json"), 1.0)
	require.NoError(t, err)
	srv := NewMultiAgentServer(map[string]*agent.Agent{}, nil)
	srv.SetSpendingLedger(ledger)

	// Under the limit the turn is admitted (it then fails for want of an
	// agent, which is not a spending refusal).
	_, err = srv.Weave(context.Background(), &loomv1.WeaveRequest{Query: "hi"})
	assert.NotEqual(t, codes.ResourceExhausted, status.Code(err))

	require.NoError(t, ledger.Record(1.25))
	_, err = srv.Weave(context.Background(), &loomv1.WeaveRequest{Query: "hi"})
	require.Equal(t, codes.ResourceExhausted, status.Code(err), "err: %v", err)
	assert.Contains(t, status.Convert(err).Message(), "limit of $1.00")
	assert.Equal(t, SpendingLimitReason, errorReason(err))

	// Without a ledger nothing is limited.
	srv.SetSpendingLedger(nil)
	_, err = srv.Weave(context.Background(), &loomv1.WeaveRequest{Query: "hi"})
	assert.NotEqual(t, codes.ResourceExhausted, status.Code(err))
}

// Over the limit, /v1/weave:stream answers with an error frame that names
// the reason, so a phone can say so in plain words.
func TestStreamWeaveSSE_SpendingLimitFrame(t *testing.T) {
	srv, llm, _ := newClientToolServer(t)
	ledger, err := spending.Open(filepath.Join(t.TempDir(), "spending.json"), 0.5)
	require.NoError(t, err)
	require.NoError(t, ledger.Record(0.5))
	srv.SetSpendingLedger(ledger)
	h := NewHTTPServer(srv, "", "", zaptest.NewLogger(t))
	ts := httptest.NewServer(http.HandlerFunc(h.handleStreamWeaveSSE))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(`{"query":"hi"}`))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(body)), "data:"))
	var frame map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(line), &frame), "body: %s", body)
	assert.Equal(t, "EXECUTION_STAGE_FAILED", frame["stage"])
	assert.Equal(t, "ResourceExhausted", frame["code"])
	assert.Equal(t, SpendingLimitReason, frame["reason"])
	advertised, _ := llm.snapshot()
	assert.Empty(t, advertised, "the agent must not run")
}
