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
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ItemKind is what sort of thing an item is.
type ItemKind string

const (
	// Chore is something to do, once or repeating.
	Chore ItemKind = "chore"
	// Turn is something the assignees take turns at.
	Turn ItemKind = "turn"
	// Pickup is a pickup or drop-off.
	Pickup ItemKind = "pickup"
	// Appointment is an appointment, for someone, with someone taking them.
	Appointment ItemKind = "appointment"
)

// Item limits.
const (
	// MaxOpenItems bounds the items not yet done, for the whole household.
	MaxOpenItems = 500
	// MaxTitleLength and MaxPlaceLength bound those fields, in characters.
	MaxTitleLength = 200
	MaxPlaceLength = 200
	// MaxItemNotesLength bounds an item's notes, in characters.
	MaxItemNotesLength = 1000
	// MaxAssignees bounds an item's assignees.
	MaxAssignees = 20
	// keepDoneFor is how long a one-time item stays after it is done.
	keepDoneFor = 30 * 24 * time.Hour
)

// Item errors.
var (
	ErrItemNotFound = errors.New("no such item")
	// ErrNotInvolved is for an item the caller can see but is not involved
	// in (it is for everyone).
	ErrNotInvolved = errors.New("only the people an item involves can change it")
	// ErrNotYours is for completing an item that is someone else's to do.
	ErrNotYours = errors.New("that is not yours to mark done")
	// ErrTooManyItems is returned past MaxOpenItems.
	ErrTooManyItems = errors.New("the household has too many open items")
)

// Repeat says when an item comes back; the zero value is once.
type Repeat struct {
	Daily bool `json:"daily,omitempty"`
	// Weekdays are 1 = Monday ... 7 = Sunday.
	Weekdays []int `json:"weekdays,omitempty"`
}

// Repeats reports whether r comes back at all.
func (r Repeat) Repeats() bool { return r.Daily || len(r.Weekdays) > 0 }

// on reports whether r includes t's day of the week.
func (r Repeat) on(t time.Time) bool {
	if r.Daily {
		return true
	}
	iso := int(t.Weekday())
	if iso == 0 {
		iso = 7
	}
	for _, d := range r.Weekdays {
		if d == iso {
			return true
		}
	}
	return false
}

// Item is one chore, turn, pickup or appointment.
type Item struct {
	ID    string   `json:"id"`
	Kind  ItemKind `json:"kind"`
	Title string   `json:"title"`
	// Assignees are user IDs, in turn order for a turn.
	Assignees []string `json:"assignees,omitempty"`
	// TurnIndex is the assignee whose turn it is (turns only).
	TurnIndex int        `json:"turn_index,omitempty"`
	ForUser   string     `json:"for_user,omitempty"`
	Everyone  bool       `json:"everyone,omitempty"`
	StartsAt  *time.Time `json:"starts_at,omitempty"`
	Repeat    Repeat     `json:"repeat,omitempty"`
	Place     string     `json:"place,omitempty"`
	Notes     string     `json:"notes,omitempty"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DoneBy    string     `json:"done_by,omitempty"`
	DoneAt    *time.Time `json:"done_at,omitempty"`
	// Done is set once a one-time item is done.
	Done bool `json:"done,omitempty"`
}

// ItemInput is what a person sets on an item.
type ItemInput struct {
	Kind      ItemKind
	Title     string
	Assignees []string
	ForUser   string
	Everyone  bool
	StartsAt  *time.Time
	Repeat    Repeat
	Place     string
	Notes     string
}

func (it Item) isAssignee(userID string) bool {
	for _, a := range it.Assignees {
		if a == userID {
			return true
		}
	}
	return false
}

// Involves reports whether userID set up it, is assigned to it, or it is for
// them. Only they may change or delete it.
func (it Item) Involves(userID string) bool {
	return it.CreatedBy == userID || it.ForUser == userID || it.isAssignee(userID)
}

// VisibleTo reports whether userID may see it.
func (it Item) VisibleTo(userID string) bool { return it.Everyone || it.Involves(userID) }

// TurnOf returns whose turn it is ("" unless a turn).
func (it Item) TurnOf() string {
	if it.Kind != Turn || len(it.Assignees) == 0 {
		return ""
	}
	return it.Assignees[it.TurnIndex%len(it.Assignees)]
}

// NextTurnOf returns whose turn is after this one ("" unless a turn).
func (it Item) NextTurnOf() string {
	if it.Kind != Turn || len(it.Assignees) == 0 {
		return ""
	}
	return it.Assignees[(it.TurnIndex+1)%len(it.Assignees)]
}

// Yours reports whether it is userID's to do: their turn, an item assigned
// to them, or one with no assignees that they can see.
func (it Item) Yours(userID string) bool {
	switch {
	case it.Kind == Turn:
		return it.TurnOf() == userID
	case len(it.Assignees) > 0:
		return it.isAssignee(userID)
	default:
		return it.VisibleTo(userID)
	}
}

// mayComplete reports whether userID may mark it done: any assignee (of a
// turn too, doing it for whoever's turn it was), or anyone who sees an item
// with no assignees.
func (it Item) mayComplete(userID string) bool {
	if len(it.Assignees) > 0 {
		return it.isAssignee(userID)
	}
	return it.VisibleTo(userID)
}

// ItemFilter narrows ListItems.
type ItemFilter struct {
	// MineOnly keeps the caller's own (Item.Yours).
	MineOnly bool
	// IncludeDone keeps one-time items already done.
	IncludeDone bool
	// From and To keep items due in [From, To) when set; items with no time
	// are always kept.
	From, To time.Time
}

func itemsPathFor(notesPath string) string {
	return filepath.Join(filepath.Dir(notesPath), "items.json")
}

func (h *Household) location() *time.Location {
	if h.loc != nil {
		return h.loc
	}
	return time.Local
}

func (h *Household) cleanItem(in ItemInput) (ItemInput, error) {
	switch in.Kind {
	case Chore, Turn, Pickup, Appointment:
	default:
		return in, fmt.Errorf("%w: kind must be chore, turn, pickup or appointment", ErrInvalidInput)
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Place = strings.TrimSpace(in.Place)
	in.Notes = strings.TrimSpace(in.Notes)
	for _, f := range []struct {
		name, value string
		max         int
	}{{"title", in.Title, MaxTitleLength}, {"place", in.Place, MaxPlaceLength}, {"notes", in.Notes, MaxItemNotesLength}} {
		if utf8.RuneCountInString(f.value) > f.max {
			return in, fmt.Errorf("%w: %s is longer than %d characters", ErrInvalidInput, f.name, f.max)
		}
	}
	if in.Title == "" {
		return in, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}
	seen := map[string]bool{}
	var assignees []string
	for _, a := range in.Assignees {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		assignees = append(assignees, a)
	}
	if len(assignees) > MaxAssignees {
		return in, fmt.Errorf("%w: at most %d people", ErrInvalidInput, MaxAssignees)
	}
	in.Assignees = assignees
	if in.Kind == Turn && len(in.Assignees) < 2 {
		return in, fmt.Errorf("%w: taking turns needs at least two people", ErrInvalidInput)
	}
	people := append([]string(nil), in.Assignees...)
	if in.ForUser != "" {
		people = append(people, in.ForUser)
	}
	for _, id := range people {
		ok, err := h.IsMember(id)
		if err != nil {
			return in, err
		}
		if !ok {
			return in, ErrUnknownPerson
		}
	}
	days := map[int]bool{}
	var weekdays []int
	for _, d := range in.Repeat.Weekdays {
		if d < 1 || d > 7 {
			return in, fmt.Errorf("%w: weekdays are 1 (Monday) to 7 (Sunday)", ErrInvalidInput)
		}
		if !days[d] {
			days[d] = true
			weekdays = append(weekdays, d)
		}
	}
	sort.Ints(weekdays)
	in.Repeat.Weekdays = weekdays
	if in.Repeat.Daily {
		in.Repeat.Weekdays = nil
	}
	if in.StartsAt != nil {
		t := in.StartsAt.UTC()
		in.StartsAt = &t
	} else if in.Repeat.Repeats() {
		// Starts on its next day, from today.
		loc := h.location()
		now := h.now().In(loc)
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		for !in.Repeat.on(day) {
			day = day.AddDate(0, 0, 1)
		}
		t := day.UTC()
		in.StartsAt = &t
	}
	return in, nil
}

// nextOccurrence returns when a repeating item due at start is due next,
// once done at now: the first day it repeats after both start's day and
// today, at start's wall-clock time. Doing an overdue item counts as doing
// today's, so missed days do not pile up; doing one early moves it past its
// due day.
func nextOccurrence(start time.Time, r Repeat, now time.Time, loc *time.Location) time.Time {
	s := start.In(loc)
	from := startOfDay(s, loc)
	if today := startOfDay(now, loc); today.After(from) {
		from = today
	}
	for i := 1; i <= 8; i++ {
		day := from.AddDate(0, 0, i)
		if r.on(day) {
			return time.Date(day.Year(), day.Month(), day.Day(), s.Hour(), s.Minute(), s.Second(), 0, loc).UTC()
		}
	}
	return start
}

// SaveItem creates an item set up by userID (id empty) or changes one they
// are involved in.
func (h *Household) SaveItem(userID, id string, in ItemInput) (Item, error) {
	in, err := h.cleanItem(in)
	if err != nil {
		return Item{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadItemsLocked(); err != nil {
		return Item{}, err
	}
	now := h.now().UTC()
	items := append([]Item(nil), h.items...)
	if id == "" {
		open := 0
		for _, it := range items {
			if !it.Done {
				open++
			}
		}
		if open >= MaxOpenItems {
			return Item{}, ErrTooManyItems
		}
		newID, err := newItemID()
		if err != nil {
			return Item{}, err
		}
		it := Item{ID: newID, CreatedBy: userID, CreatedAt: now}
		applyInput(&it, in)
		it.UpdatedAt = now
		items = append(items, it)
		if err := h.saveItemsLocked(items); err != nil {
			return Item{}, err
		}
		return it, nil
	}
	i := indexItem(items, id)
	if i < 0 || !items[i].VisibleTo(userID) {
		return Item{}, ErrItemNotFound
	}
	if !items[i].Involves(userID) {
		return Item{}, ErrNotInvolved
	}
	it := items[i]
	turn := it.TurnOf()
	applyInput(&it, in)
	it.TurnIndex = 0
	for j, a := range it.Assignees {
		if a == turn {
			it.TurnIndex = j
		}
	}
	it.UpdatedAt = now
	items[i] = it
	if err := h.saveItemsLocked(items); err != nil {
		return Item{}, err
	}
	return it, nil
}

func applyInput(it *Item, in ItemInput) {
	it.Kind, it.Title, it.Assignees, it.ForUser, it.Everyone = in.Kind, in.Title, in.Assignees, in.ForUser, in.Everyone
	it.StartsAt, it.Repeat, it.Place, it.Notes = in.StartsAt, in.Repeat, in.Place, in.Notes
}

// CompleteItem marks an item done by userID: a turn passes to the next
// assignee, a repeating item moves to its next date, and a one-time item is
// done.
func (h *Household) CompleteItem(userID, id string) (Item, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadItemsLocked(); err != nil {
		return Item{}, err
	}
	items := append([]Item(nil), h.items...)
	i := indexItem(items, id)
	if i < 0 || !items[i].VisibleTo(userID) {
		return Item{}, ErrItemNotFound
	}
	it := items[i]
	if !it.mayComplete(userID) {
		return Item{}, ErrNotYours
	}
	now := h.now().UTC()
	it.DoneBy, it.DoneAt, it.UpdatedAt = userID, &now, now
	if it.Kind == Turn {
		it.TurnIndex = (it.TurnIndex + 1) % len(it.Assignees)
	}
	switch {
	case it.Repeat.Repeats() && it.StartsAt != nil:
		next := nextOccurrence(*it.StartsAt, it.Repeat, now, h.location())
		it.StartsAt = &next
	case it.Kind != Turn:
		it.Done = true
	}
	items[i] = it
	if err := h.saveItemsLocked(items); err != nil {
		return Item{}, err
	}
	return it, nil
}

func startOfDay(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

func endOfDay(t time.Time, loc *time.Location) time.Time {
	return startOfDay(t, loc).AddDate(0, 0, 1).Add(-time.Nanosecond)
}

// DeleteItem deletes an item userID is involved in.
func (h *Household) DeleteItem(userID, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadItemsLocked(); err != nil {
		return err
	}
	i := indexItem(h.items, id)
	if i < 0 || !h.items[i].VisibleTo(userID) {
		return ErrItemNotFound
	}
	if !h.items[i].Involves(userID) {
		return ErrNotInvolved
	}
	items := append(append([]Item(nil), h.items[:i]...), h.items[i+1:]...)
	return h.saveItemsLocked(items)
}

// ListItems returns the items userID may see, soonest first, items with no
// time last.
func (h *Household) ListItems(userID string, f ItemFilter) ([]Item, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadItemsLocked(); err != nil {
		return nil, err
	}
	var out []Item
	for _, it := range h.items {
		if !it.VisibleTo(userID) || it.Done && !f.IncludeDone || f.MineOnly && !it.Yours(userID) {
			continue
		}
		if it.StartsAt != nil {
			if !f.From.IsZero() && it.StartsAt.Before(f.From) || !f.To.IsZero() && !it.StartsAt.Before(f.To) {
				continue
			}
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].StartsAt, out[j].StartsAt
		switch {
		case a == nil || b == nil:
			return a != nil && b == nil
		default:
			return a.Before(*b)
		}
	})
	return out, nil
}

// GetItem returns one item userID may see.
func (h *Household) GetItem(userID, id string) (Item, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.loadItemsLocked(); err != nil {
		return Item{}, err
	}
	i := indexItem(h.items, id)
	if i < 0 || !h.items[i].VisibleTo(userID) {
		return Item{}, ErrItemNotFound
	}
	return h.items[i], nil
}

// itemUpdates counts userID's open items due by the end of today and the
// latest change to an item they can see.
func (h *Household) itemUpdatesLocked(userID string) (due int, updated time.Time, err error) {
	if err := h.loadItemsLocked(); err != nil {
		return 0, time.Time{}, err
	}
	end := endOfDay(h.now(), h.location())
	for _, it := range h.items {
		if !it.VisibleTo(userID) {
			continue
		}
		if it.UpdatedAt.After(updated) {
			updated = it.UpdatedAt
		}
		if !it.Done && it.Yours(userID) && it.StartsAt != nil && !it.StartsAt.After(end) {
			due++
		}
	}
	return due, updated, nil
}

func indexItem(items []Item, id string) int {
	for i := range items {
		if items[i].ID == id {
			return i
		}
	}
	return -1
}

func (h *Household) loadItemsLocked() error {
	if h.itemsLoaded {
		return nil
	}
	data, err := os.ReadFile(h.itemsPath) // #nosec G304 -- operator-configured data directory
	switch {
	case errors.Is(err, os.ErrNotExist):
		h.items, h.itemsLoaded = nil, true
		return nil
	case err != nil:
		return fmt.Errorf("read items: %w", err)
	}
	var stored struct {
		Items []Item `json:"items"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("read items %s: %w", h.itemsPath, err)
	}
	h.items, h.itemsLoaded = stored.Items, true
	return nil
}

// saveItemsLocked drops one-time items done more than keepDoneFor ago,
// writes items and, once written, makes them current.
func (h *Household) saveItemsLocked(items []Item) error {
	cutoff := h.now().Add(-keepDoneFor)
	kept := items[:0:0]
	for _, it := range items {
		if it.Done && it.DoneAt != nil && it.DoneAt.Before(cutoff) {
			continue
		}
		kept = append(kept, it)
	}
	data, err := json.MarshalIndent(struct {
		Items []Item `json:"items"`
	}{kept}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(h.itemsPath, data); err != nil {
		return fmt.Errorf("save items: %w", err)
	}
	h.items = kept
	return nil
}

func newItemID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "item_" + hex.EncodeToString(b), nil
}
