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
package mobile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
)

func TestNewEngine_RejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	mutate := func(f func(*loomv1.MobileEngineConfig)) string {
		cfg := validConfig(dir)
		f(cfg)
		return configJSON(t, cfg)
	}
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{"not JSON", "{", "invalid config"},
		{"unknown field", `{"data_dirr":"/x"}`, "invalid config"},
		{"missing data_dir", mutate(func(c *loomv1.MobileEngineConfig) { c.DataDir = "" }), "data_dir is required"},
		{"relative data_dir", mutate(func(c *loomv1.MobileEngineConfig) { c.DataDir = "relative/dir" }), "absolute path"},
		{"negative memory bound", mutate(func(c *loomv1.MobileEngineConfig) { c.ToolResultMemoryBytes = -1 }), "must not be negative"},
		{"missing agent", mutate(func(c *loomv1.MobileEngineConfig) { c.Agent = nil }), "agent is required"},
		{"missing name", mutate(func(c *loomv1.MobileEngineConfig) { c.Agent.Name = "" }), "agent.name is required"},
		{"blank system prompt", mutate(func(c *loomv1.MobileEngineConfig) { c.Agent.SystemPrompt = "  " }), "agent.system_prompt is required"},
		{
			"model configured in agent",
			mutate(func(c *loomv1.MobileEngineConfig) { c.Agent.Llm = &loomv1.LLMConfig{Model: "m"} }),
			"not supported by the embedded engine: agent.llm",
		},
		{
			"several unsupported agent fields are all named",
			mutate(func(c *loomv1.MobileEngineConfig) {
				c.Agent.Tools = &loomv1.ToolsConfig{Builtin: []string{"shell_execute"}}
				c.Agent.ActiveProvider = "anthropic"
			}),
			"agent.active_provider, agent.tools",
		},
		{
			"unsupported behavior field",
			mutate(func(c *loomv1.MobileEngineConfig) { c.Agent.Behavior = &loomv1.BehaviorConfig{TimeoutSeconds: 5} }),
			"agent.behavior.timeout_seconds",
		},
		{
			"negative behavior limit",
			mutate(func(c *loomv1.MobileEngineConfig) { c.Agent.Behavior = &loomv1.BehaviorConfig{MaxTurns: -1} }),
			"limits must not be negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := NewEngine(tt.config, &scriptedModel{respond: lookupThenAnswer})
			require.Error(t, err)
			assert.Nil(t, e)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	t.Run("nil model", func(t *testing.T) {
		e, err := NewEngine(configJSON(t, validConfig(dir)), nil)
		require.Error(t, err)
		assert.Nil(t, e)
		assert.Contains(t, err.Error(), "model provider is required")
	})
}

func TestNewEngine_AppliesBehaviorLimits(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.Agent.Description = "Finds trails"
	cfg.Agent.Behavior = &loomv1.BehaviorConfig{MaxTurns: 3, MaxToolExecutions: 4, MaxIterations: 2}

	got := agentConfig(cfg)
	assert.Equal(t, "trail-guide", got.Name)
	assert.Equal(t, "Finds trails", got.Description)
	assert.Equal(t, testPrompt, got.SystemPrompt)
	assert.Equal(t, 3, got.MaxTurns)
	assert.Equal(t, 4, got.MaxToolExecutions)
	assert.Equal(t, 2, got.MaxIterations)
	assert.False(t, got.EnableTracing)

	defaults := agentConfig(validConfig(t.TempDir()))
	assert.Positive(t, defaults.MaxTurns, "unset limits keep the agent defaults")
	assert.Positive(t, defaults.MaxToolExecutions)
	assert.Positive(t, defaults.MaxIterations)
}

func TestNewEngine_SetsDataDirOnlyWhenUnset(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv(dataDirEnv, "")
		dir := t.TempDir()
		newTestEngine(t, dir, &scriptedModel{respond: lookupThenAnswer})
		assert.Equal(t, dir, os.Getenv(dataDirEnv))
	})
	t.Run("already set", func(t *testing.T) {
		t.Setenv(dataDirEnv, "/chosen/by/the/host")
		newTestEngine(t, t.TempDir(), &scriptedModel{respond: lookupThenAnswer})
		assert.Equal(t, "/chosen/by/the/host", os.Getenv(dataDirEnv))
	})
}

func TestWeave_RunsToolLoop(t *testing.T) {
	model := &scriptedModel{respond: lookupThenAnswer}
	tool := trailTool(trailFacts)
	e := newTestEngine(t, t.TempDir(), model)
	require.NoError(t, e.RegisterTool(tool))

	out, err := e.Weave(weaveRequest(t, "session-1", "Corbet's Couloir"))
	require.NoError(t, err)

	resp := decodeWeaveResponse(t, out)
	assert.Equal(t, "session-1", resp.GetSessionId())
	assert.Contains(t, resp.GetText(), `"max_pitch_deg":34`)
	assert.EqualValues(t, 1, tool.calls.Load())

	requests := model.recorded()
	require.Len(t, requests, 2, "one call to request the tool, one to answer")

	var spec *loomv1.HostToolSpec
	for _, s := range requests[0].GetTools() {
		if s.GetName() == testToolName {
			spec = s
		}
	}
	require.NotNil(t, spec, "the host tool is offered to the model")
	assert.Equal(t, "object", spec.GetInputSchema().GetFields()["type"].GetStringValue())
	assert.Contains(t, spec.GetInputSchema().GetFields()["properties"].GetStructValue().GetFields(), "name")

	var system []string
	for _, m := range requests[0].GetMessages() {
		if m.GetRole() == "system" {
			system = append(system, m.GetContent())
		}
	}
	assert.Contains(t, strings.Join(system, "\n"), testPrompt, "the configured system prompt reaches the model")

	final := requests[1].GetMessages()
	result := final[len(final)-1]
	assert.Equal(t, "tool", result.GetRole())
	assert.NotEmpty(t, result.GetToolUseId())
	assert.Empty(t, result.GetToolError())
	assert.EqualValues(t, 34, result.GetToolResult().GetStructValue().GetFields()["max_pitch_deg"].GetNumberValue())
}

func TestWeave_GeneratesSessionID(t *testing.T) {
	e := newTestEngine(t, t.TempDir(), &scriptedModel{respond: answerOnly("hello")})

	out, err := e.Weave(weaveRequest(t, "", "hi"))
	require.NoError(t, err)
	assert.NotEmpty(t, decodeWeaveResponse(t, out).GetSessionId())
}

func TestWeave_SessionSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	first, err := NewEngine(configJSON(t, validConfig(dir)), &scriptedModel{respond: answerOnly("noted")})
	require.NoError(t, err)
	_, err = first.Weave(weaveRequest(t, "session-1", "my home mountain is Alta"))
	require.NoError(t, err)
	require.NoError(t, first.Close())

	model := &scriptedModel{respond: answerOnly("Alta")}
	second := newTestEngine(t, dir, model)
	_, err = second.Weave(weaveRequest(t, "session-1", "what is my home mountain?"))
	require.NoError(t, err)

	requests := model.recorded()
	require.NotEmpty(t, requests)
	var seen []string
	for _, m := range requests[0].GetMessages() {
		seen = append(seen, m.GetContent())
	}
	assert.Contains(t, strings.Join(seen, "\n"), "my home mountain is Alta",
		"a new engine over the same data directory restores the earlier turn")
}

func TestWeave_ToolFailureReachesModel(t *testing.T) {
	tests := []struct {
		name    string
		execute func(string) (string, error)
		wantErr string
	}{
		{"host error", func(string) (string, error) { return "", errors.New("trail database offline") }, "trail database offline"},
		{"invalid JSON", func(string) (string, error) { return "not json", nil }, "not valid JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &scriptedModel{respond: lookupThenAnswer}
			e := newTestEngine(t, t.TempDir(), model)
			require.NoError(t, e.RegisterTool(trailTool(tt.execute)))

			out, err := e.Weave(weaveRequest(t, "s", "Corbet's Couloir"))
			require.NoError(t, err, "a failed tool is reported to the model, not to the caller")
			assert.Contains(t, decodeWeaveResponse(t, out).GetText(), tt.wantErr)
		})
	}
}

func TestWeave_ToolWithNoData(t *testing.T) {
	model := &scriptedModel{respond: lookupThenAnswer}
	e := newTestEngine(t, t.TempDir(), model)
	require.NoError(t, e.RegisterTool(trailTool(func(string) (string, error) { return "", nil })))

	out, err := e.Weave(weaveRequest(t, "s", "Corbet's Couloir"))
	require.NoError(t, err)
	assert.Contains(t, decodeWeaveResponse(t, out).GetText(), "trail facts: null")
}

func TestWeave_ModelFailures(t *testing.T) {
	tests := []struct {
		name    string
		respond func(*loomv1.HostModelRequest) (*loomv1.HostModelResponse, error)
		wantErr string
	}{
		{
			"host model error",
			func(*loomv1.HostModelRequest) (*loomv1.HostModelResponse, error) {
				return nil, errors.New("no network")
			},
			"no network",
		},
		{
			"tool call without id",
			func(*loomv1.HostModelRequest) (*loomv1.HostModelResponse, error) {
				return &loomv1.HostModelResponse{ToolCalls: []*loomv1.HostToolCall{{Name: testToolName}}}, nil
			},
			"has no id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEngine(t, t.TempDir(), &scriptedModel{respond: tt.respond})
			_, err := e.Weave(weaveRequest(t, "s", "hi"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestWeave_RejectsInvalidRequest(t *testing.T) {
	e := newTestEngine(t, t.TempDir(), &scriptedModel{respond: answerOnly("x")})
	tests := []struct {
		name    string
		request string
		wantErr string
	}{
		{"not JSON", "{", "invalid request"},
		{"unknown field", `{"quary":"hi"}`, "invalid request"},
		{"empty query", `{"session_id":"s"}`, "InvalidArgument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.Weave(tt.request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)

			err = e.StreamWeave(tt.request, &progressRecorder{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestWeave_AcceptsCamelCaseFieldNames(t *testing.T) {
	e := newTestEngine(t, t.TempDir(), &scriptedModel{respond: answerOnly("hello")})

	out, err := e.Weave(`{"sessionId":"camel","query":"hi"}`)
	require.NoError(t, err)
	assert.Equal(t, "camel", decodeWeaveResponse(t, out).GetSessionId())
	assert.Contains(t, out, `"session_id"`, "output uses the names written in the .proto files")
}

func TestStreamWeave_ReportsProgressAndAnswer(t *testing.T) {
	e := newTestEngine(t, t.TempDir(), &scriptedModel{respond: lookupThenAnswer})
	require.NoError(t, e.RegisterTool(trailTool(trailFacts)))

	rec := &progressRecorder{}
	require.NoError(t, e.StreamWeave(weaveRequest(t, "s", "Corbet's Couloir"), rec))

	events := rec.recorded(t)
	require.NotEmpty(t, events)
	last := events[len(events)-1]
	assert.Equal(t, loomv1.ExecutionStage_EXECUTION_STAGE_COMPLETED, last.GetStage())
	assert.Contains(t, last.GetPartialContent(), `"max_pitch_deg":34`)
	assert.EqualValues(t, 10, last.GetCost().GetLlmCost().GetOutputTokens(), "usage reported by the host model is carried through")

	var sawTool bool
	for _, ev := range events {
		if ev.GetToolName() == testToolName {
			sawTool = true
		}
	}
	assert.True(t, sawTool, "tool execution is reported as progress")
}

func TestStreamWeave_RequiresListener(t *testing.T) {
	e := newTestEngine(t, t.TempDir(), &scriptedModel{respond: answerOnly("x")})
	err := e.StreamWeave(weaveRequest(t, "s", "hi"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "progress listener is required")
}

func TestRegisterTool_Validation(t *testing.T) {
	e := newTestEngine(t, t.TempDir(), &scriptedModel{respond: answerOnly("x")})
	builtin := e.agent.ListTools()
	require.NotEmpty(t, builtin, "the agent registers tools of its own")

	tests := []struct {
		name    string
		tool    HostTool
		wantErr string
	}{
		{"nil", nil, "tool is required"},
		{"no name", &fakeTool{description: "d", schema: testSchema}, "tool name is required"},
		{"no description", &fakeTool{name: "t", schema: testSchema}, "description is required"},
		{"schema not JSON", &fakeTool{name: "t", description: "d", schema: "{"}, "not valid JSON"},
		{"schema not an object", &fakeTool{name: "t", description: "d", schema: `{"type":"string"}`}, `must be "object"`},
		{"collides with an agent tool", &fakeTool{name: builtin[0], description: "d", schema: testSchema}, "already registered"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.RegisterTool(tt.tool)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	t.Run("duplicate", func(t *testing.T) {
		require.NoError(t, e.RegisterTool(trailTool(trailFacts)))
		err := e.RegisterTool(trailTool(trailFacts))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already registered")
	})
}

func TestClose_IsIdempotentAndStopsTheEngine(t *testing.T) {
	e, err := NewEngine(configJSON(t, validConfig(t.TempDir())), &scriptedModel{respond: answerOnly("x")})
	require.NoError(t, err)
	require.NoError(t, e.Close())
	require.NoError(t, e.Close())

	_, err = e.Weave(weaveRequest(t, "s", "hi"))
	assert.ErrorIs(t, err, errClosed)
	assert.ErrorIs(t, e.StreamWeave(weaveRequest(t, "s", "hi"), &progressRecorder{}), errClosed)
	assert.ErrorIs(t, e.RegisterTool(trailTool(trailFacts)), errClosed)
}

func TestClose_WaitsForTurnInFlight(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	model := &scriptedModel{respond: func(*loomv1.HostModelRequest) (*loomv1.HostModelResponse, error) {
		once.Do(func() { close(entered) })
		<-release
		return &loomv1.HostModelResponse{Content: "late", StopReason: "end_turn"}, nil
	}}
	e, err := NewEngine(configJSON(t, validConfig(t.TempDir())), model)
	require.NoError(t, err)

	weaveDone := make(chan struct{})
	go func() {
		defer close(weaveDone)
		_, _ = e.Weave(weaveRequest(t, "s", "hi"))
	}()
	<-entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- e.Close() }()

	select {
	case <-closeDone:
		t.Fatal("Close returned while a turn was still inside a host callback")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return after the host callback finished")
	}
	<-weaveDone
}

func TestWeave_ConcurrentSessions(t *testing.T) {
	e := newTestEngine(t, t.TempDir(), &scriptedModel{respond: lookupThenAnswer})
	require.NoError(t, e.RegisterTool(trailTool(trailFacts)))

	const sessions = 8
	errs := make(chan error, sessions)
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := e.Weave(weaveRequest(t, fmt.Sprintf("session-%d", i), "Corbet's Couloir"))
			if err == nil && !strings.Contains(out, "max_pitch_deg") {
				err = fmt.Errorf("session %d: unexpected response %s", i, out)
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestTracing_WritesTraceDatabaseWhenEnabled(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{"enabled", true},
		{"disabled", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := validConfig(dir)
			cfg.EnableTracing = tt.enabled
			e, err := NewEngine(configJSON(t, cfg), &scriptedModel{respond: answerOnly("hello")})
			require.NoError(t, err)
			_, err = e.Weave(weaveRequest(t, "s", "hi"))
			require.NoError(t, err)
			require.NoError(t, e.Close())

			_, statErr := os.Stat(filepath.Join(dir, traceDBName))
			if tt.enabled {
				assert.NoError(t, statErr)
			} else {
				assert.ErrorIs(t, statErr, os.ErrNotExist)
			}
			_, statErr = os.Stat(filepath.Join(dir, sessionDBName))
			assert.NoError(t, statErr)
		})
	}
}

// answerOnly is a host model that always answers with text.
func answerOnly(text string) func(*loomv1.HostModelRequest) (*loomv1.HostModelResponse, error) {
	return func(*loomv1.HostModelRequest) (*loomv1.HostModelResponse, error) {
		return &loomv1.HostModelResponse{Content: text, StopReason: "end_turn"}, nil
	}
}
