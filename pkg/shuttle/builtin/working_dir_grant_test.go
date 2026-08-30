// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package builtin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/teradata-labs/loom/pkg/session"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

// grantTestRoot creates a directory tree outside the temp locations that the shell and
// file tools allow independently of any grant. On Linux the OS temp directory is /tmp,
// which is whitelisted, so a grant escape rooted in t.TempDir() would be allowed there
// for reasons unrelated to grant containment.
func grantTestRoot(t *testing.T) string {
	t.Helper()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home directory: %v", err)
	}
	root, err := os.MkdirTemp(home, ".loom-grant-test-")
	if err != nil {
		t.Skipf("cannot create test root under %s: %v", home, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	return root
}

// isolateLoomDataDir points LOOM_DATA_DIR at an empty directory unrelated to the grant
// roots so that the data-directory allowance never masks a containment failure.
func isolateLoomDataDir(t *testing.T) string {
	t.Helper()

	dataDir := filepath.Join(t.TempDir(), "loom-data")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	t.Setenv("LOOM_DATA_DIR", dataDir)
	t.Setenv("LOOM_SANDBOX_DIR", dataDir)

	return dataDir
}

func mkdirIn(t *testing.T, parent, name string) string {
	t.Helper()

	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	return dir
}

func TestIsWithinDir(t *testing.T) {
	sep := string(filepath.Separator)

	tests := []struct {
		name string
		path string
		dir  string
		want bool
	}{
		{name: "same directory", path: sep + "repo", dir: sep + "repo", want: true},
		{name: "child", path: filepath.Join(sep+"repo", "pkg", "main.go"), dir: sep + "repo", want: true},
		{name: "sibling with shared prefix", path: sep + "repo-evil", dir: sep + "repo", want: false},
		{name: "parent", path: sep, dir: sep + "repo", want: false},
		{name: "unrelated", path: sep + "other", dir: sep + "repo", want: false},
		{name: "empty dir", path: sep + "repo", dir: "", want: false},
		{name: "empty path", path: "", dir: sep + "repo", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWithinDir(tt.path, tt.dir); got != tt.want {
				t.Errorf("isWithinDir(%q, %q) = %v, want %v", tt.path, tt.dir, got, tt.want)
			}
		})
	}
}

func TestShellExecuteWithWorkingDirGrant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on a POSIX shell")
	}

	isolateLoomDataDir(t)

	root := grantTestRoot(t)
	grant := mkdirIn(t, root, "repo")
	nested := mkdirIn(t, grant, "sub")
	sibling := mkdirIn(t, root, "repo-evil")
	outside := mkdirIn(t, root, "outside")

	realGrant := evalSymlinks(t, grant)
	realNested := evalSymlinks(t, nested)

	tests := []struct {
		name       string
		sessionID  string
		grant      string
		params     map[string]interface{}
		wantCode   string
		wantStdout string
	}{
		{
			name:       "grant becomes the default working directory",
			grant:      grant,
			params:     map[string]interface{}{"command": "pwd"},
			wantStdout: realGrant,
		},
		{
			name:       "relative working_dir resolves under the grant",
			grant:      grant,
			params:     map[string]interface{}{"command": "pwd", "working_dir": "sub"},
			wantStdout: realNested,
		},
		{
			name:     "absolute working_dir outside the grant is restricted without a session",
			grant:    grant,
			params:   map[string]interface{}{"command": "pwd", "working_dir": outside},
			wantCode: "PATH_RESTRICTED",
		},
		{
			name:     "sibling directory sharing the grant prefix is restricted",
			grant:    grant,
			params:   map[string]interface{}{"command": "pwd", "working_dir": sibling},
			wantCode: "PATH_RESTRICTED",
		},
		{
			name:      "absolute working_dir outside the grant is restricted with a session",
			sessionID: "sess-1",
			grant:     grant,
			params:    map[string]interface{}{"command": "pwd", "working_dir": outside},
			wantCode:  "PATH_RESTRICTED",
		},
		{
			name:       "no grant and no session stays unrestricted",
			params:     map[string]interface{}{"command": "pwd", "working_dir": outside},
			wantStdout: evalSymlinks(t, outside),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewShellExecuteTool(root)

			ctx := context.Background()
			if tt.sessionID != "" {
				ctx = session.WithSessionID(ctx, tt.sessionID)
			}
			ctx = session.ContextWithWorkingDir(ctx, tt.grant)

			result, err := tool.Execute(ctx, tt.params)
			if err != nil {
				t.Fatalf("Execute() error: %v", err)
			}

			if tt.wantCode != "" {
				if result.Success {
					t.Fatalf("Execute() succeeded, want error %s", tt.wantCode)
				}
				if result.Error == nil || result.Error.Code != tt.wantCode {
					t.Fatalf("Execute() error = %+v, want code %s", result.Error, tt.wantCode)
				}
				return
			}

			if !result.Success {
				t.Fatalf("Execute() failed: %+v", result.Error)
			}
			stdout := dataField(t, result, "stdout")
			if got := evalSymlinks(t, strings.TrimSpace(stdout)); got != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", got, tt.wantStdout)
			}
		})
	}
}

func TestShellExecuteExportsWorkingDirGrantToEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on a POSIX shell")
	}

	isolateLoomDataDir(t)
	grant := mkdirIn(t, grantTestRoot(t), "repo")

	tests := []struct {
		name  string
		grant string
		want  string
	}{
		{name: "grant is exported", grant: grant, want: grant},
		{name: "no grant exports nothing", grant: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewShellExecuteTool(grant)
			ctx := session.ContextWithWorkingDir(context.Background(), tt.grant)

			result, err := tool.Execute(ctx, map[string]interface{}{
				"command":     "printf '%s' \"$LOOM_WORKING_DIR\"",
				"working_dir": grant,
			})
			if err != nil {
				t.Fatalf("Execute() error: %v", err)
			}
			if !result.Success {
				t.Fatalf("Execute() failed: %+v", result.Error)
			}
			if got := dataField(t, result, "stdout"); got != tt.want {
				t.Errorf("LOOM_WORKING_DIR = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileWriteAndReadWithWorkingDirGrant(t *testing.T) {
	isolateLoomDataDir(t)

	root := grantTestRoot(t)
	grant := mkdirIn(t, root, "repo")
	mkdirIn(t, root, "repo-evil")

	writer := NewFileWriteTool(root)
	reader := NewFileReadTool(root)
	ctx := session.ContextWithWorkingDir(context.Background(), grant)

	t.Run("round trip inside the grant", func(t *testing.T) {
		writeResult, err := writer.Execute(ctx, map[string]interface{}{
			"path":    filepath.Join("out", "data.txt"),
			"content": "granted",
			"mode":    "overwrite",
		})
		if err != nil {
			t.Fatalf("write error: %v", err)
		}
		if !writeResult.Success {
			t.Fatalf("write failed: %+v", writeResult.Error)
		}
		wantPath := filepath.Join(grant, "out", "data.txt")
		if got := dataField(t, writeResult, "path"); got != wantPath {
			t.Errorf("written path = %q, want %q", got, wantPath)
		}

		readResult, err := reader.Execute(ctx, map[string]interface{}{
			"path": filepath.Join("out", "data.txt"),
		})
		if err != nil {
			t.Fatalf("read error: %v", err)
		}
		if !readResult.Success {
			t.Fatalf("read failed: %+v", readResult.Error)
		}
		if got := dataField(t, readResult, "content"); got != "granted" {
			t.Errorf("content = %q, want %q", got, "granted")
		}
	})

	escapes := []struct {
		name string
		path string
	}{
		{name: "parent traversal", path: filepath.Join("..", "outside", "data.txt")},
		{name: "sibling sharing the grant prefix", path: filepath.Join("..", "repo-evil", "data.txt")},
		{name: "saturating traversal to an absolute system path", path: strings.Repeat("../", 16) + "etc/hosts"},
		{name: "absolute path outside the allowed set", path: filepath.Join(root, "outside", "data.txt")},
	}

	for _, tt := range escapes {
		t.Run("write rejects "+tt.name, func(t *testing.T) {
			result, err := writer.Execute(ctx, map[string]interface{}{
				"path":    tt.path,
				"content": "escaped",
				"mode":    "overwrite",
			})
			if err != nil {
				t.Fatalf("Execute() error: %v", err)
			}
			assertPathRestricted(t, result, grant)
		})

		t.Run("read rejects "+tt.name, func(t *testing.T) {
			result, err := reader.Execute(ctx, map[string]interface{}{"path": tt.path})
			if err != nil {
				t.Fatalf("Execute() error: %v", err)
			}
			assertPathRestricted(t, result, grant)
		})
	}
}

func TestFileToolsWithoutGrantUseBaseDir(t *testing.T) {
	isolateLoomDataDir(t)

	baseDir := t.TempDir()
	writer := NewFileWriteTool(baseDir)
	reader := NewFileReadTool(baseDir)
	ctx := context.Background()

	writeResult, err := writer.Execute(ctx, map[string]interface{}{
		"path":    "notes.txt",
		"content": "ungranted",
		"mode":    "overwrite",
	})
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	if !writeResult.Success {
		t.Fatalf("write failed: %+v", writeResult.Error)
	}
	wantPath := filepath.Join(baseDir, "notes.txt")
	if got := dataField(t, writeResult, "path"); got != wantPath {
		t.Errorf("written path = %q, want %q", got, wantPath)
	}

	readResult, err := reader.Execute(ctx, map[string]interface{}{"path": "notes.txt"})
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if !readResult.Success {
		t.Fatalf("read failed: %+v", readResult.Error)
	}
	if got := dataField(t, readResult, "content"); got != "ungranted" {
		t.Errorf("content = %q, want %q", got, "ungranted")
	}
}

func assertPathRestricted(t *testing.T, result *shuttle.Result, grant string) {
	t.Helper()

	if result.Success {
		t.Fatalf("Execute() succeeded, want PATH_RESTRICTED")
	}
	if result.Error == nil || result.Error.Code != "PATH_RESTRICTED" {
		t.Fatalf("Execute() error = %+v, want code PATH_RESTRICTED", result.Error)
	}
	if !strings.Contains(result.Error.Suggestion, grant) {
		t.Errorf("suggestion %q does not name the granted directory %q", result.Error.Suggestion, grant)
	}
}

func dataField(t *testing.T, result *shuttle.Result, key string) string {
	t.Helper()

	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("result.Data is %T, want map[string]interface{}", result.Data)
	}
	value, _ := data[key].(string)

	return value
}

func evalSymlinks(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}

	return resolved
}
