// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package session

import (
	"context"
	"testing"
)

func TestWorkingDirContextRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want string
	}{
		{name: "absolute path", dir: "/repos/loom", want: "/repos/loom"},
		{name: "relative path stored verbatim", dir: "repos/loom", want: "repos/loom"},
		{name: "empty grant leaves context unchanged", dir: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := ContextWithWorkingDir(context.Background(), tt.dir)
			if got := WorkingDirFromContext(ctx); got != tt.want {
				t.Errorf("WorkingDirFromContext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWorkingDirFromContextAbsent(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{name: "background context", ctx: context.Background()},
		{name: "context with session id only", ctx: WithSessionID(context.Background(), "sess-1")},
		{name: "string key is not honored", ctx: context.WithValue(context.Background(), "working_dir", "/repos/loom")}, //nolint:staticcheck // asserts the typed key is the only accepted carrier
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WorkingDirFromContext(tt.ctx); got != "" {
				t.Errorf("WorkingDirFromContext() = %q, want empty", got)
			}
		})
	}
}

func TestWorkingDirIsIndependentOfSessionID(t *testing.T) {
	ctx := WithSessionID(context.Background(), "sess-42")
	ctx = ContextWithWorkingDir(ctx, "/repos/loom")

	if got := SessionIDFromContext(ctx); got != "sess-42" {
		t.Errorf("SessionIDFromContext() = %q, want %q", got, "sess-42")
	}
	if got := WorkingDirFromContext(ctx); got != "/repos/loom" {
		t.Errorf("WorkingDirFromContext() = %q, want %q", got, "/repos/loom")
	}
}
