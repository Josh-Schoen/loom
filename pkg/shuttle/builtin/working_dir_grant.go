// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package builtin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/teradata-labs/loom/pkg/config"
)

// isWithinDir reports whether path is dir itself or a descendant of dir.
// Both arguments must already be cleaned absolute paths. A bare prefix test would
// accept /repo-evil as being inside /repo, so the separator is required.
func isWithinDir(path, dir string) bool {
	if dir == "" || path == "" {
		return false
	}
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// osTempDirs returns the temp locations filesystem tools allow alongside a grant.
func osTempDirs() []string {
	var dirs []string
	if runtime.GOOS == "windows" {
		if temp := os.Getenv("TEMP"); temp != "" {
			if abs, err := filepath.Abs(temp); err == nil {
				dirs = append(dirs, abs)
			}
		}
	} else {
		dirs = append(dirs, "/tmp")
	}
	if abs, err := filepath.Abs(os.TempDir()); err == nil {
		dirs = append(dirs, abs)
	}
	return dirs
}

// resolveGrantedPath resolves a tool's path parameter against an active
// working-directory grant and reports whether the result stays inside the allowed
// set: the grant subtree, LOOM_DATA_DIR, or the OS temp locations.
//
// Symlinks are resolved on the deepest existing ancestor so that a path whose final
// components do not exist yet (a file about to be written) is still checked against
// its real location rather than a link that escapes the grant.
func resolveGrantedPath(path, grant string) (resolved string, allowed bool) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(grant, path)
	}
	resolved = filepath.Clean(path)
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}

	real := realPathOfDeepestExistingAncestor(resolved)

	absGrant := grant
	if abs, err := filepath.Abs(grant); err == nil {
		absGrant = abs
	}
	if realGrant, err := filepath.EvalSymlinks(absGrant); err == nil {
		absGrant = realGrant
	}

	if isWithinDir(real, absGrant) {
		return resolved, true
	}

	if dataDir := config.GetLoomDataDir(); dataDir != "" {
		if absData, err := filepath.Abs(dataDir); err == nil {
			if realData, err := filepath.EvalSymlinks(absData); err == nil {
				absData = realData
			}
			if isWithinDir(real, absData) {
				return resolved, true
			}
		}
	}

	for _, tmp := range osTempDirs() {
		if realTmp, err := filepath.EvalSymlinks(tmp); err == nil {
			tmp = realTmp
		}
		if isWithinDir(real, tmp) {
			return resolved, true
		}
	}

	return resolved, false
}

// realPathOfDeepestExistingAncestor resolves symlinks on the longest existing
// prefix of path and re-appends the components that do not exist yet.
func realPathOfDeepestExistingAncestor(path string) string {
	remainder := ""
	current := path
	for {
		if real, err := filepath.EvalSymlinks(current); err == nil {
			if remainder == "" {
				return real
			}
			return filepath.Join(real, remainder)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}
