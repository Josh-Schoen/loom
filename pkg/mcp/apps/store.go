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

package apps

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// appNamePattern is the dynamic app name format enforced by the RPCs and agent
// tools. The store checks it again because names become file names.
var appNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// fileStore persists dynamic apps as one JSON file per app, so apps an agent
// created survive server restarts. DefaultOwner's apps are in the directory
// itself (where apps were stored before they had owners); every other
// owner's are in users/<key>/, where key is derived from the owner ID so any
// ID is a safe directory name.
type fileStore struct {
	dir string
}

// usersDir holds the per-owner directories.
const usersDir = "users"

// storedApp is the on-disk form of a dynamic app.
type storedApp struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	HTML        string          `json:"html"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Spec        json.RawMessage `json:"spec,omitempty"`
	Owner       string          `json:"owner,omitempty"` // empty means DefaultOwner

	file string // path relative to the store directory, set by loadAll
}

// ownerKey is the directory name for an owner's apps: the first 32 hex
// characters of the SHA-256 of the owner ID.
func ownerKey(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(sum[:])[:32]
}

// ownerDir is the directory holding owner's apps.
func (s *fileStore) ownerDir(owner string) string {
	if normalizeOwner(owner) == DefaultOwner {
		return s.dir
	}
	return filepath.Join(s.dir, usersDir, ownerKey(owner))
}

func newFileStore(dir string) (*fileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create apps dir: %w", err)
	}
	return &fileStore{dir: dir}, nil
}

func (s *fileStore) path(owner, name string) (string, error) {
	if !appNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid app name for storage: %q", name)
	}
	return filepath.Join(s.ownerDir(owner), name+".json"), nil
}

// save writes res atomically (temp file, then rename) with owner-only permissions.
func (s *fileStore) save(res *UIResource) error {
	name := extractAppName(res.URI)
	owner := normalizeOwner(res.Owner)
	path, err := s.path(owner, name)
	if err != nil {
		return err
	}
	stored := storedApp{
		Name:        name,
		DisplayName: res.Name,
		Description: res.Description,
		HTML:        string(res.HTML),
		UpdatedAt:   res.UpdatedAt,
		Spec:        json.RawMessage(res.Spec),
	}
	if owner != DefaultOwner {
		stored.Owner = owner
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("save app %s: %w", name, err)
	}
	tmp, err := os.CreateTemp(dir, ".app-*.json")
	if err != nil {
		return fmt.Errorf("save app %s: %w", name, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save app %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save app %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save app %s: %w", name, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save app %s: %w", name, err)
	}
	return nil
}

func (s *fileStore) delete(owner, name string) error {
	path, err := s.path(owner, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete app %s: %w", name, err)
	}
	return nil
}

// loadAll reads every stored app: DefaultOwner's in the directory itself and
// every other owner's under users/. Unreadable or malformed files, and files
// whose owner does not match their directory, are reported in skipped (as
// paths relative to the store directory) and left in place for inspection.
func (s *fileStore) loadAll() (loaded []storedApp, skipped []string, err error) {
	loaded, skipped, err = s.loadDir("", func(owner string) bool {
		return normalizeOwner(owner) == DefaultOwner
	})
	if err != nil {
		return nil, nil, err
	}
	owners, err := os.ReadDir(filepath.Join(s.dir, usersDir))
	if errors.Is(err, os.ErrNotExist) {
		return loaded, skipped, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read apps dir: %w", err)
	}
	for _, e := range owners {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		apps, bad, err := s.loadDir(filepath.Join(usersDir, key), func(owner string) bool {
			return owner != "" && normalizeOwner(owner) != DefaultOwner && ownerKey(owner) == key
		})
		if err != nil {
			skipped = append(skipped, filepath.Join(usersDir, key))
			continue
		}
		loaded = append(loaded, apps...)
		skipped = append(skipped, bad...)
	}
	return loaded, skipped, nil
}

// loadDir reads the app files directly in rel (relative to the store
// directory), keeping those whose owner passes ownerOK.
func (s *fileStore) loadDir(rel string, ownerOK func(owner string) bool) (loaded []storedApp, skipped []string, err error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, rel))
	if err != nil {
		return nil, nil, fmt.Errorf("read apps dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		file := filepath.Join(rel, e.Name())
		data, err := os.ReadFile(filepath.Join(s.dir, file)) // #nosec G304 -- file in the server's own apps dir
		if err != nil {
			skipped = append(skipped, file)
			continue
		}
		var app storedApp
		if err := json.Unmarshal(data, &app); err != nil || !appNamePattern.MatchString(app.Name) ||
			app.Name+".json" != e.Name() || app.HTML == "" || !ownerOK(app.Owner) {
			skipped = append(skipped, file)
			continue
		}
		app.file = file
		loaded = append(loaded, app)
	}
	return loaded, skipped, nil
}

// specBytes returns a stored spec, or nil when none was stored.
func specBytes(raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return append([]byte(nil), raw...)
}
