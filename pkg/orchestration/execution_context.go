// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package orchestration

import "context"

// ExecutionInfo identifies the scheduled run a workflow execution belongs to.
// The scheduler sets it on the execution context so task tracking can stamp
// the run's board with it — without it, boards are orphans that no run
// history can find again.
type ExecutionInfo struct {
	ScheduleID  string
	ExecutionID string
}

type executionInfoKey struct{}

// WithExecutionInfo returns a context carrying the run's identity.
func WithExecutionInfo(ctx context.Context, info ExecutionInfo) context.Context {
	return context.WithValue(ctx, executionInfoKey{}, info)
}

// ExecutionInfoFrom returns the run identity on the context, if any.
func ExecutionInfoFrom(ctx context.Context) (ExecutionInfo, bool) {
	info, ok := ctx.Value(executionInfoKey{}).(ExecutionInfo)
	return info, ok
}
