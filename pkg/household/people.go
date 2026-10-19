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

// Package household keeps what the people who share one server (a family
// using an assistant on their computer) share with each other: their names
// and the notes they send each other. Each person reads only the notes they
// sent or received.
package household

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// PeopleFileName is the names file in the data directory: a JSON object of
// user ID to display name ({"sam-3f9a0c71": "Sam"}). `looms pair --name`
// writes it, and so may a setup app that adds people.
const PeopleFileName = "people.json"

// MaxNameLength bounds a display name, in characters.
const MaxNameLength = 60

// People reads and writes the names file. It rereads the file when it
// changes on disk, so names another program writes show up without a
// restart. Safe for concurrent use within one process.
type People struct {
	path string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	names   map[string]string
}

// NewPeople keeps names in path (usually <data dir>/people.json).
func NewPeople(path string) *People {
	return &People{path: path}
}

// Names returns a copy of the user ID → name map. A missing file is no names.
func (p *People) Names() (map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.refreshLocked(); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(p.names))
	for id, name := range p.names {
		out[id] = name
	}
	return out, nil
}

// Name returns the display name for userID, or userID when it has none.
func (p *People) Name(userID string) string {
	names, err := p.Names()
	if err == nil {
		if name := names[userID]; name != "" {
			return name
		}
	}
	return userID
}

// SetName gives userID a display name, replacing any it had.
func (p *People) SetName(userID, name string) error {
	name = strings.TrimSpace(name)
	switch {
	case userID == "":
		return errors.New("user id is required")
	case name == "":
		return errors.New("name is required")
	case utf8.RuneCountInString(name) > MaxNameLength:
		return fmt.Errorf("name is longer than %d characters", MaxNameLength)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.refreshLocked(); err != nil {
		return err
	}
	names := make(map[string]string, len(p.names)+1)
	for id, n := range p.names {
		names[id] = n
	}
	names[userID] = name
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(p.path, data); err != nil {
		return fmt.Errorf("save names: %w", err)
	}
	p.names = names
	p.modTime = time.Time{} // reread next time, to pick up the new mtime
	return nil
}

func (p *People) refreshLocked() error {
	info, err := os.Stat(p.path)
	if errors.Is(err, os.ErrNotExist) {
		p.names, p.modTime, p.size = map[string]string{}, time.Time{}, 0
		return nil
	}
	if err != nil {
		return fmt.Errorf("read names: %w", err)
	}
	if p.names != nil && info.ModTime().Equal(p.modTime) && info.Size() == p.size {
		return nil
	}
	data, err := os.ReadFile(p.path) // #nosec G304 -- operator-configured data directory
	if err != nil {
		return fmt.Errorf("read names: %w", err)
	}
	names := map[string]string{}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &names); err != nil {
			return fmt.Errorf("read names %s: %w", p.path, err)
		}
	}
	p.names, p.modTime, p.size = names, info.ModTime(), info.Size()
	return nil
}

// writeFileAtomic writes data to path with owner-only permissions through a
// temporary file, so readers never see half a file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
