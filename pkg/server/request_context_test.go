// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

// noteRecordingLLM records the system messages of every Chat call.
type noteRecordingLLM struct {
	mu      sync.Mutex
	systems []string
}

func (m *noteRecordingLLM) Chat(_ context.Context, messages []llmtypes.Message, _ []shuttle.Tool) (*llmtypes.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range messages {
		if msg.Role == "system" {
			m.systems = append(m.systems, msg.Content)
		}
	}
	return &llmtypes.LLMResponse{Content: "ok", Usage: llmtypes.Usage{InputTokens: 1, OutputTokens: 1}}, nil
}

func (m *noteRecordingLLM) Name() string  { return "note-recorder" }
func (m *noteRecordingLLM) Model() string { return "mock" }

// WeaveRequest.context reaches the model as a note for that request.
func TestWeaveRequestContextReachesTheModel(t *testing.T) {
	llm := &noteRecordingLLM{}
	cfg := agent.DefaultConfig()
	cfg.PatternConfig = agent.DefaultPatternConfig()
	cfg.PatternConfig.UseLLMClassifier = false
	cfg.Name = "assistant"
	ag := agent.NewAgent(&mockBackend{}, llm, agent.WithConfig(cfg))
	srv := NewMultiAgentServer(map[string]*agent.Agent{"assistant": ag}, nil)

	_, err := srv.Weave(context.Background(), &loomv1.WeaveRequest{
		Query:   "What day is it?",
		AgentId: "assistant",
		Context: map[string]string{"local_time": "2026-10-18T07:05:00-05:00", "time_zone": "America/Chicago"},
	})
	require.NoError(t, err)

	llm.mu.Lock()
	defer llm.mu.Unlock()
	var found bool
	for _, s := range llm.systems {
		if s == "Context sent with this message (applies to this message only):\n- local_time: 2026-10-18T07:05:00-05:00\n- time_zone: America/Chicago" {
			found = true
		}
	}
	assert.True(t, found, "system messages sent: %q", llm.systems)
}
