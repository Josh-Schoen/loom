// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
)

// A run's stages must survive the store round-trip intact: the receipt a UI
// draws from history has to be the receipt the scheduler recorded.
func TestStore_RecordExecutionWithStages(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	defer func() { _ = store.Close() }()

	schedule := &loomv1.ScheduledWorkflow{
		Id:           "sched-stages",
		WorkflowName: "staged",
		Pattern: &loomv1.WorkflowPattern{
			Pattern: &loomv1.WorkflowPattern_Pipeline{
				Pipeline: &loomv1.PipelinePattern{InitialPrompt: "p"},
			},
		},
		Schedule:  &loomv1.ScheduleConfig{Cron: "0 8 * * *", Timezone: "UTC", Enabled: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	require.NoError(t, store.Create(ctx, schedule))

	exec := &loomv1.ScheduleExecution{
		ExecutionId: "exec-1",
		StartedAt:   time.Now().Unix(),
		CompletedAt: time.Now().Unix(),
		Status:      "success",
		DurationMs:  4200,
		WorkflowId:  "wf-1",
		Output:      "3 products moved >10%",
		BoardId:     "board-1",
		Stages: []*loomv1.StageExecution{
			{Stage: 1, AgentId: "data-analyst", DurationMs: 2800, SessionId: "wf-1-stage1-data-analyst", TotalTokens: 1204, CostUsd: 0.021},
			{Stage: 2, AgentId: "formatter", DurationMs: 1100, SessionId: "wf-1-stage2-formatter", TotalTokens: 512, CostUsd: 0.004},
		},
	}
	require.NoError(t, store.RecordExecution(ctx, exec, schedule.Id))

	history, err := store.GetExecutionHistory(ctx, schedule.Id, 10)
	require.NoError(t, err)
	require.Len(t, history, 1)
	got := history[0]
	require.Len(t, got.Stages, 2)
	assert.Equal(t, int32(1), got.Stages[0].Stage)
	assert.Equal(t, "data-analyst", got.Stages[0].AgentId)
	assert.Equal(t, int64(2800), got.Stages[0].DurationMs)
	assert.Equal(t, "wf-1-stage1-data-analyst", got.Stages[0].SessionId)
	assert.Equal(t, int32(1204), got.Stages[0].TotalTokens)
	assert.InDelta(t, 0.021, got.Stages[0].CostUsd, 1e-9)
	assert.Equal(t, "formatter", got.Stages[1].AgentId)
	assert.Equal(t, "3 products moved >10%", got.Output)
	assert.Equal(t, "board-1", got.BoardId)
}

// A run recorded without stages (a failed run, or history written before the
// stages column existed) must read back as a plain row, not an error.
func TestStore_RecordExecutionWithoutStages(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	defer func() { _ = store.Close() }()

	schedule := &loomv1.ScheduledWorkflow{
		Id:           "sched-plain",
		WorkflowName: "plain",
		Pattern: &loomv1.WorkflowPattern{
			Pattern: &loomv1.WorkflowPattern_Pipeline{
				Pipeline: &loomv1.PipelinePattern{InitialPrompt: "p"},
			},
		},
		Schedule:  &loomv1.ScheduleConfig{Cron: "0 8 * * *", Timezone: "UTC", Enabled: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	require.NoError(t, store.Create(ctx, schedule))

	exec := &loomv1.ScheduleExecution{
		ExecutionId: "exec-2",
		StartedAt:   time.Now().Unix(),
		Status:      "failed",
		Error:       "boom",
	}
	require.NoError(t, store.RecordExecution(ctx, exec, schedule.Id))

	history, err := store.GetExecutionHistory(ctx, schedule.Id, 10)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Empty(t, history[0].Stages)
	assert.Equal(t, "boom", history[0].Error)
}

// stagesFromResult maps agent results to receipt rows: executor stage numbers
// win over result order, session ids come from metadata, cost is optional.
func TestStagesFromResult(t *testing.T) {
	assert.Nil(t, stagesFromResult(nil))
	assert.Nil(t, stagesFromResult(&loomv1.WorkflowResult{}))

	result := &loomv1.WorkflowResult{
		AgentResults: []*loomv1.AgentResult{
			{
				AgentId:    "analyst",
				DurationMs: 2800,
				Metadata:   map[string]string{"stage": "1", "session_id": "wf-stage1-analyst"},
				Cost:       &loomv1.AgentExecutionCost{TotalTokens: 900, CostUsd: 0.01},
			},
			{
				// No stage metadata and no cost: position falls back to
				// result order, cost stays zero.
				AgentId:    "formatter",
				DurationMs: 1100,
				Metadata:   map[string]string{"session_id": "wf-stage2-formatter"},
			},
		},
	}

	stages := stagesFromResult(result)
	require.Len(t, stages, 2)
	assert.Equal(t, int32(1), stages[0].Stage)
	assert.Equal(t, "wf-stage1-analyst", stages[0].SessionId)
	assert.Equal(t, int32(900), stages[0].TotalTokens)
	assert.Equal(t, int32(2), stages[1].Stage)
	assert.Equal(t, "formatter", stages[1].AgentId)
	assert.Zero(t, stages[1].TotalTokens)
}

// capOutput must cut long output without producing invalid UTF-8.
func TestCapOutput(t *testing.T) {
	assert.Equal(t, "short", capOutput("short"))
	long := strings.Repeat("é", maxOutputBytes) // 2 bytes per rune
	capped := capOutput(long)
	assert.LessOrEqual(t, len(capped), maxOutputBytes+len("\n… [output truncated]"))
	assert.True(t, strings.HasSuffix(capped, "[output truncated]"))
	assert.True(t, utf8.ValidString(capped))
}

// The workflow id names every stage session, so it must be a key the run
// record also carries — the execution id for fresh runs, the schedule id for
// RESUME continuity. A private executor uuid is exactly what this replaces.
func TestRunWorkflowID(t *testing.T) {
	if got := runWorkflowID(loomv1.ScheduledSessionMode_SCHEDULED_SESSION_MODE_NEW, "sched", "exec"); got != "exec" {
		t.Errorf("NEW = %q, want the execution id", got)
	}
	if got := runWorkflowID(loomv1.ScheduledSessionMode_SCHEDULED_SESSION_MODE_UNSPECIFIED, "sched", "exec"); got != "exec" {
		t.Errorf("UNSPECIFIED = %q, want the execution id", got)
	}
	if got := runWorkflowID(loomv1.ScheduledSessionMode_SCHEDULED_SESSION_MODE_RESUME, "sched", "exec"); got != "sched" {
		t.Errorf("RESUME = %q, want the stable schedule id", got)
	}
}
