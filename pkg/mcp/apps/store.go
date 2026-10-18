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

// fileStore persists dynamic apps as one JSON file per app in a directory, so
// apps an agent created survive server restarts.
type fileStore struct {
	dir string
}

// storedApp is the on-disk form of a dynamic app.
type storedApp struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	HTML        string          `json:"html"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Spec        json.RawMessage `json:"spec,omitempty"`
}

func newFileStore(dir string) (*fileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create apps dir: %w", err)
	}
	return &fileStore{dir: dir}, nil
}

func (s *fileStore) path(name string) (string, error) {
	if !appNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid app name for storage: %q", name)
	}
	return filepath.Join(s.dir, name+".json"), nil
}

// save writes res atomically (temp file, then rename) with owner-only permissions.
func (s *fileStore) save(res *UIResource) error {
	name := extractAppName(res.URI)
	path, err := s.path(name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(storedApp{
		Name:        name,
		DisplayName: res.Name,
		Description: res.Description,
		HTML:        string(res.HTML),
		UpdatedAt:   res.UpdatedAt,
		Spec:        json.RawMessage(res.Spec),
	})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".app-*.json")
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

func (s *fileStore) delete(name string) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete app %s: %w", name, err)
	}
	return nil
}

// loadAll reads every stored app. Unreadable or malformed files are reported
// in skipped and left in place for inspection.
func (s *fileStore) loadAll() (loaded []storedApp, skipped []string, err error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read apps dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name())) // #nosec G304 -- file in the server's own apps dir
		if err != nil {
			skipped = append(skipped, e.Name())
			continue
		}
		var app storedApp
		if err := json.Unmarshal(data, &app); err != nil || !appNamePattern.MatchString(app.Name) ||
			app.Name+".json" != e.Name() || app.HTML == "" {
			skipped = append(skipped, e.Name())
			continue
		}
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
