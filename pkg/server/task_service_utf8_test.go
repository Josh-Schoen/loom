// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/teradata-labs/loom/pkg/task"
)

// A task whose notes carry raw tool bytes (invalid UTF-8) must still marshal:
// one bad row made every ListTasks on its board fail with Internal, which the
// desktop polls — the whole surface read as "server down".
func TestTaskToProtoSanitizesInvalidUTF8(t *testing.T) {
	bad := "result: \xff\xfe compressed \x80 bytes"
	p := taskToProto(&task.Task{
		ID:          "t1",
		Title:       "Stage 1: analyst",
		Description: bad,
		Notes:       bad,
	})

	if _, err := proto.Marshal(p); err != nil {
		t.Fatalf("sanitized task failed to marshal: %v", err)
	}
	if p.Notes == bad {
		t.Error("notes were not sanitized")
	}
}

func TestUTF8CleanPassesValidStringsUntouched(t *testing.T) {
	s := "plain ✓ text with é"
	if got := utf8Clean(s); got != s {
		t.Errorf("valid string changed: %q", got)
	}
}
