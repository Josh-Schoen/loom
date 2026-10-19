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

package script

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"go.starlark.net/syntax"
)

// SavedScript is a calculation script kept for one person, to run again by
// name.
type SavedScript struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Script      string    `json:"script"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store limits.
const (
	MaxSavedScripts      = 50
	maxDescriptionLength = 300
)

// ErrScriptNotFound is returned for a name the person has no script under.
var ErrScriptNotFound = errors.New("no saved script by that name")

var scriptName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Store keeps each person's saved scripts in one JSON file per person under
// a directory, named by a hash of the person's ID, written with owner-only
// permissions. Safe for concurrent use within one process.
type Store struct {
	mu    sync.Mutex
	dir   string
	limit Limits
}

// NewStore keeps scripts under dir. limits bound the script length.
func NewStore(dir string, limits Limits) *Store {
	return &Store{dir: dir, limit: limits}
}

func (s *Store) file(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])[:32]+".json")
}

func (s *Store) loadLocked(owner string) (map[string]SavedScript, error) {
	data, err := os.ReadFile(s.file(owner)) // #nosec G304 -- name derived from a hash
	if errors.Is(err, os.ErrNotExist) {
		return map[string]SavedScript{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read saved scripts: %w", err)
	}
	var list []SavedScript
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("read saved scripts: %w", err)
	}
	out := make(map[string]SavedScript, len(list))
	for _, sc := range list {
		out[sc.Name] = sc
	}
	return out, nil
}

func (s *Store) saveLocked(owner string, scripts map[string]SavedScript) error {
	list := make([]SavedScript, 0, len(scripts))
	for _, sc := range scripts {
		list = append(list, sc)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("save scripts: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".scripts-*.json")
	if err != nil {
		return fmt.Errorf("save scripts: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save scripts: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save scripts: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save scripts: %w", err)
	}
	return os.Rename(tmp.Name(), s.file(owner))
}

// CheckSyntax reports a parse error in src, as the run would.
func CheckSyntax(src string) error {
	if _, err := fileOptions.Parse("script", src, 0); err != nil {
		var syntaxErr syntax.Error
		if errors.As(err, &syntaxErr) {
			return fmt.Errorf("syntax error at line %d: %s", syntaxErr.Pos.Line, syntaxErr.Msg)
		}
		return err
	}
	return nil
}

// Save keeps sc for owner, replacing a script with the same name.
func (s *Store) Save(owner string, sc SavedScript) error {
	if !scriptName.MatchString(sc.Name) {
		return errors.New("name must be lowercase letters, numbers and hyphens, starting with a letter or number (at most 63)")
	}
	if len(sc.Description) > maxDescriptionLength {
		return fmt.Errorf("description must be at most %d characters", maxDescriptionLength)
	}
	if s.limit.MaxScriptBytes > 0 && len(sc.Script) > s.limit.MaxScriptBytes {
		return fmt.Errorf("the script is longer than %d characters", s.limit.MaxScriptBytes)
	}
	if err := CheckSyntax(sc.Script); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scripts, err := s.loadLocked(owner)
	if err != nil {
		return err
	}
	if _, exists := scripts[sc.Name]; !exists && len(scripts) >= MaxSavedScripts {
		return fmt.Errorf("there are already %d saved scripts; delete one first", MaxSavedScripts)
	}
	sc.UpdatedAt = time.Now().UTC()
	scripts[sc.Name] = sc
	return s.saveLocked(owner, scripts)
}

// Get returns owner's script called name.
func (s *Store) Get(owner, name string) (SavedScript, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scripts, err := s.loadLocked(owner)
	if err != nil {
		return SavedScript{}, err
	}
	sc, ok := scripts[name]
	if !ok {
		return SavedScript{}, ErrScriptNotFound
	}
	return sc, nil
}

// List returns owner's scripts by name.
func (s *Store) List(owner string) ([]SavedScript, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scripts, err := s.loadLocked(owner)
	if err != nil {
		return nil, err
	}
	out := make([]SavedScript, 0, len(scripts))
	for _, sc := range scripts {
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete removes owner's script called name.
func (s *Store) Delete(owner, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	scripts, err := s.loadLocked(owner)
	if err != nil {
		return err
	}
	if _, ok := scripts[name]; !ok {
		return ErrScriptNotFound
	}
	delete(scripts, name)
	return s.saveLocked(owner, scripts)
}
