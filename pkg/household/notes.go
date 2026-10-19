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

package household

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Kind says whether a note expects an answer.
type Kind string

const (
	// Tell passes something on; no answer expected.
	Tell Kind = "tell"
	// Ask expects one answer from the recipient.
	Ask Kind = "ask"
)

// Limits.
const (
	// MaxNoteLength bounds a note's text and an answer, in characters.
	MaxNoteLength = 1000
	// MaxNotesPerPerson is how many notes are kept per person (sent and
	// received); sending more drops that person's oldest.
	MaxNotesPerPerson = 2000
)

// Errors callers can tell apart.
var (
	ErrNotFound        = errors.New("no such note")
	ErrUnknownPerson   = errors.New("that person is not in the household")
	ErrSelf            = errors.New("a note cannot be sent to yourself")
	ErrNotAnswerable   = errors.New("only an ask can be answered")
	ErrAlreadyAnswered = errors.New("that ask was already answered")
	// ErrInvalidInput wraps problems with the text or kind of a request.
	ErrInvalidInput = errors.New("invalid input")
)

// Note is a note from one person to another.
type Note struct {
	ID           string     `json:"id"`
	From         string     `json:"from"`
	To           string     `json:"to"`
	Kind         Kind       `json:"kind"`
	Text         string     `json:"text"`
	CreatedAt    time.Time  `json:"created_at"`
	ReadAt       *time.Time `json:"read_at,omitempty"`
	Answer       string     `json:"answer,omitempty"`
	AnsweredAt   *time.Time `json:"answered_at,omitempty"`
	AnswerSeenAt *time.Time `json:"answer_seen_at,omitempty"`
}

// Involves reports whether userID sent or received n.
func (n Note) Involves(userID string) bool { return n.From == userID || n.To == userID }

// Updates is what is new for one person.
type Updates struct {
	UnreadNotes    int
	UnansweredAsks int
	NewAnswers     int
	// ItemsDue counts the person's open items due today or overdue.
	ItemsDue int
	// UpdatedAt is the latest change to a note or item the person can see; zero
	// when there is none.
	UpdatedAt time.Time
}

// Members lists the user IDs in the household.
type Members interface {
	UserIDs() []string
}

// Household keeps the notes in one JSON file, written with owner-only
// permissions. Safe for concurrent use within one process.
type Household struct {
	path    string
	people  *People
	members Members
	now     func() time.Time

	// loc is the time zone for days (repeats, "due today"); nil is the
	// server's local time.
	loc       *time.Location
	itemsPath string

	mu          sync.Mutex
	notes       []Note // oldest first
	loaded      bool
	items       []Item
	itemsLoaded bool
}

// New keeps notes in notesPath and items (chores, turns, pickups,
// appointments) in items.json beside it. people names members; members
// lists who is in the household besides the people named in people (nil
// for only those).
func New(notesPath string, people *People, members Members) *Household {
	return &Household{path: notesPath, itemsPath: itemsPathFor(notesPath), people: people, members: members, now: time.Now}
}

// People returns the names.
func (h *Household) People() *People { return h.people }

// Member is a person in the household.
type Member struct {
	UserID string
	Name   string
}

// MemberList returns everyone in the household, by name: the people with a
// name, and the users members lists.
func (h *Household) MemberList() ([]Member, error) {
	names, err := h.people.Names()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Member
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		name := names[id]
		if name == "" {
			name = id
		}
		out = append(out, Member{UserID: id, Name: name})
	}
	for id := range names {
		add(id)
	}
	if h.members != nil {
		for _, id := range h.members.UserIDs() {
			add(id)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].UserID < out[j].UserID
	})
	return out, nil
}

// IsMember reports whether userID is in the household.
func (h *Household) IsMember(userID string) (bool, error) {
	members, err := h.MemberList()
	if err != nil {
		return false, err
	}
	for _, m := range members {
		if m.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func cleanText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("%w: text is required", ErrInvalidInput)
	}
	if utf8.RuneCountInString(text) > MaxNoteLength {
		return "", fmt.Errorf("%w: text is longer than %d characters", ErrInvalidInput, MaxNoteLength)
	}
	return text, nil
}

// Send keeps a note from one member to another.
func (h *Household) Send(from, to string, kind Kind, text string) (Note, error) {
	if kind != Tell && kind != Ask {
		return Note{}, fmt.Errorf("%w: kind must be %q or %q", ErrInvalidInput, Tell, Ask)
	}
	text, err := cleanText(text)
	if err != nil {
		return Note{}, err
	}
	if from == to {
		return Note{}, ErrSelf
	}
	for _, id := range []string{from, to} {
		ok, err := h.IsMember(id)
		if err != nil {
			return Note{}, err
		}
		if !ok {
			return Note{}, ErrUnknownPerson
		}
	}
	id, err := newNoteID()
	if err != nil {
		return Note{}, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadLocked(); err != nil {
		return Note{}, err
	}
	n := Note{ID: id, From: from, To: to, Kind: kind, Text: text, CreatedAt: h.now().UTC()}
	notes := append(append([]Note(nil), h.notes...), n)
	notes = pruneOldest(notes, from)
	notes = pruneOldest(notes, to)
	if err := h.saveLocked(notes); err != nil {
		return Note{}, err
	}
	return n, nil
}

// pruneOldest drops userID's oldest notes beyond MaxNotesPerPerson.
func pruneOldest(notes []Note, userID string) []Note {
	count := 0
	for _, n := range notes {
		if n.Involves(userID) {
			count++
		}
	}
	extra := count - MaxNotesPerPerson
	if extra <= 0 {
		return notes
	}
	out := notes[:0:0]
	for _, n := range notes {
		if extra > 0 && n.Involves(userID) {
			extra--
			continue
		}
		out = append(out, n)
	}
	return out
}

// List returns up to limit notes userID sent or received, newest first.
func (h *Household) List(userID string, limit int) ([]Note, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadLocked(); err != nil {
		return nil, err
	}
	var out []Note
	for i := len(h.notes) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if h.notes[i].Involves(userID) {
			out = append(out, h.notes[i])
		}
	}
	return out, nil
}

// Answer answers an ask sent to userID. The note counts as read.
func (h *Household) Answer(userID, noteID, text string) (Note, error) {
	text, err := cleanText(text)
	if err != nil {
		return Note{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadLocked(); err != nil {
		return Note{}, err
	}
	i := h.indexLocked(noteID)
	if i < 0 || h.notes[i].To != userID {
		return Note{}, ErrNotFound
	}
	n := h.notes[i]
	if n.Kind != Ask {
		return Note{}, ErrNotAnswerable
	}
	if n.AnsweredAt != nil {
		return Note{}, ErrAlreadyAnswered
	}
	now := h.now().UTC()
	n.Answer, n.AnsweredAt = text, &now
	if n.ReadAt == nil {
		n.ReadAt = &now
	}
	notes := append([]Note(nil), h.notes...)
	notes[i] = n
	if err := h.saveLocked(notes); err != nil {
		return Note{}, err
	}
	return n, nil
}

// MarkRead marks notes sent to userID read and answers to userID's asks
// seen: the notes in ids, or all of them when ids is empty. Unknown IDs and
// other people's notes are skipped. Returns how many changed.
func (h *Household) MarkRead(userID string, ids []string) (int, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadLocked(); err != nil {
		return 0, err
	}
	now := h.now().UTC()
	notes := append([]Note(nil), h.notes...)
	changed := 0
	for i, n := range notes {
		if len(want) > 0 && !want[n.ID] {
			continue
		}
		switch {
		case n.To == userID && n.ReadAt == nil:
			notes[i].ReadAt = &now
			changed++
		case n.From == userID && n.AnsweredAt != nil && n.AnswerSeenAt == nil:
			notes[i].AnswerSeenAt = &now
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	if err := h.saveLocked(notes); err != nil {
		return 0, err
	}
	return changed, nil
}

// Updates returns what is new for userID.
func (h *Household) Updates(userID string) (Updates, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadLocked(); err != nil {
		return Updates{}, err
	}
	var u Updates
	for _, n := range h.notes {
		if !n.Involves(userID) {
			continue
		}
		if n.To == userID {
			if n.ReadAt == nil {
				u.UnreadNotes++
			}
			if n.Kind == Ask && n.AnsweredAt == nil {
				u.UnansweredAsks++
			}
		}
		if n.From == userID && n.AnsweredAt != nil && n.AnswerSeenAt == nil {
			u.NewAnswers++
		}
		for _, t := range []*time.Time{&n.CreatedAt, n.ReadAt, n.AnsweredAt, n.AnswerSeenAt} {
			if t != nil && t.After(u.UpdatedAt) {
				u.UpdatedAt = *t
			}
		}
	}
	due, updated, err := h.itemUpdatesLocked(userID)
	if err != nil {
		return Updates{}, err
	}
	u.ItemsDue = due
	if updated.After(u.UpdatedAt) {
		u.UpdatedAt = updated
	}
	return u, nil
}

func (h *Household) indexLocked(id string) int {
	for i := range h.notes {
		if h.notes[i].ID == id {
			return i
		}
	}
	return -1
}

func (h *Household) loadLocked() error {
	if h.loaded {
		return nil
	}
	data, err := os.ReadFile(h.path) // #nosec G304 -- operator-configured data directory
	switch {
	case errors.Is(err, os.ErrNotExist):
		h.notes, h.loaded = nil, true
		return nil
	case err != nil:
		return fmt.Errorf("read notes: %w", err)
	}
	var stored struct {
		Notes []Note `json:"notes"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("read notes %s: %w", h.path, err)
	}
	h.notes, h.loaded = stored.Notes, true
	return nil
}

// saveLocked writes notes and, once written, makes them current.
func (h *Household) saveLocked(notes []Note) error {
	data, err := json.MarshalIndent(struct {
		Notes []Note `json:"notes"`
	}{notes}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(h.path, data); err != nil {
		return fmt.Errorf("save notes: %w", err)
	}
	h.notes = notes
	return nil
}

func newNoteID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "note_" + hex.EncodeToString(b), nil
}
