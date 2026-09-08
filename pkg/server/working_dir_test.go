// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"os"
	"path/filepath"
	"testing"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
)

func TestWorkingDirFromRequest(t *testing.T) {
	grantDir := t.TempDir()

	nestedDir := filepath.Join(grantDir, "nested")
	if err := os.Mkdir(nestedDir, 0o750); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	regularFile := filepath.Join(grantDir, "file.txt")
	if err := os.WriteFile(regularFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tests := []struct {
		name    string
		req     *loomv1.WeaveRequest
		want    string
		wantErr bool
	}{
		{
			name: "nil request",
			req:  nil,
			want: "",
		},
		{
			name: "nil context map",
			req:  &loomv1.WeaveRequest{Query: "hi"},
			want: "",
		},
		{
			name: "context map without working_dir",
			req:  &loomv1.WeaveRequest{Query: "hi", Context: map[string]string{"other": "value"}},
			want: "",
		},
		{
			name: "empty working_dir",
			req:  &loomv1.WeaveRequest{Query: "hi", Context: map[string]string{"working_dir": ""}},
			want: "",
		},
		{
			name:    "relative path rejected",
			req:     &loomv1.WeaveRequest{Query: "hi", Context: map[string]string{"working_dir": "relative/repo"}},
			wantErr: true,
		},
		{
			name:    "nonexistent directory rejected",
			req:     &loomv1.WeaveRequest{Query: "hi", Context: map[string]string{"working_dir": filepath.Join(grantDir, "missing")}},
			wantErr: true,
		},
		{
			name:    "file is not a directory",
			req:     &loomv1.WeaveRequest{Query: "hi", Context: map[string]string{"working_dir": regularFile}},
			wantErr: true,
		},
		{
			name: "valid directory accepted",
			req:  &loomv1.WeaveRequest{Query: "hi", Context: map[string]string{"working_dir": grantDir}},
			want: grantDir,
		},
		{
			name: "valid directory is cleaned",
			req:  &loomv1.WeaveRequest{Query: "hi", Context: map[string]string{"working_dir": filepath.Join(grantDir, "nested", "..", "nested")}},
			want: nestedDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workingDirFromRequest(tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("workingDirFromRequest() error = nil, want error (got %q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("workingDirFromRequest() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("workingDirFromRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}
