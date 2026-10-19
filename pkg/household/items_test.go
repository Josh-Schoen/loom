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
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teradata-labs/loom/pkg/types"
)

const (
	alex = "alex-1"
	sam  = "sam-2"
	jo   = "jo-3"
)

// chicago is the household's time zone in these tests.
var chicago = func() *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		panic(err)
	}
	return loc
}()

// familyAt is family with a clock at now, in Chicago.
func familyAt(t *testing.T, now *time.Time) *Household {
	t.Helper()
	h := family(t)
	h.loc = chicago
	h.now = func() time.Time { return *now }
	return h
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, chicago)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr(t time.Time) *time.Time { return &t }

func TestItemVisibility(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)

	dentist, err := h.SaveItem(alex, "", ItemInput{Kind: Appointment, Title: "Dentist", ForUser: jo, StartsAt: ptr(at("2026-10-20 16:00")), Place: "Main St"})
	require.NoError(t, err)
	bins, err := h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "Bins", Everyone: true, Repeat: Repeat{Weekdays: []int{2}}})
	require.NoError(t, err)
	private, err := h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "Call the bank"})
	require.NoError(t, err)

	ids := func(user string) []string {
		items, err := h.ListItems(user, ItemFilter{})
		require.NoError(t, err)
		var out []string
		for _, it := range items {
			out = append(out, it.Title)
		}
		return out
	}
	assert.ElementsMatch(t, []string{"Dentist", "Bins", "Call the bank"}, ids(alex))
	assert.ElementsMatch(t, []string{"Dentist", "Bins"}, ids(jo), "Jo sees what is for Jo and what is for everyone")
	assert.Equal(t, []string{"Bins"}, ids(sam), "Sam sees only what is for everyone")

	_, err = h.GetItem(sam, private.ID)
	assert.ErrorIs(t, err, ErrItemNotFound)
	assert.ErrorIs(t, h.DeleteItem(sam, private.ID), ErrItemNotFound)
	assert.ErrorIs(t, h.DeleteItem(sam, bins.ID), ErrNotInvolved, "seeing an everyone item is not being involved in it")
	_, err = h.SaveItem(sam, dentist.ID, ItemInput{Kind: Appointment, Title: "Cancelled"})
	assert.ErrorIs(t, err, ErrItemNotFound)

	// Jo is involved in the dentist (it is for Jo) and may change it.
	_, err = h.SaveItem(jo, dentist.ID, ItemInput{Kind: Appointment, Title: "Dentist (moved)", ForUser: jo, StartsAt: ptr(at("2026-10-21 16:00"))})
	require.NoError(t, err)
	require.NoError(t, h.DeleteItem(jo, dentist.ID))
	assert.ElementsMatch(t, []string{"Bins", "Call the bank"}, ids(alex))
}

func TestItemRejects(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)
	cases := map[string]ItemInput{
		"no kind":        {Title: "x"},
		"no title":       {Kind: Chore},
		"stranger":       {Kind: Chore, Title: "x", Assignees: []string{"stranger"}},
		"stranger for":   {Kind: Appointment, Title: "x", ForUser: "stranger"},
		"turn of one":    {Kind: Turn, Title: "Dishes", Assignees: []string{sam, sam}},
		"bad weekday":    {Kind: Chore, Title: "x", Repeat: Repeat{Weekdays: []int{8}}},
		"title too long": {Kind: Chore, Title: strings.Repeat("x", MaxTitleLength+1)},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := h.SaveItem(alex, "", in)
			assert.Error(t, err)
		})
	}
}

// Dishes rotate between Sam, Jo and Alex, every night at 7.
func TestTurnRotatesAndRepeats(t *testing.T) {
	now := at("2026-10-19 09:00") // Monday
	h := familyAt(t, &now)
	dishes, err := h.SaveItem(alex, "", ItemInput{Kind: Turn, Title: "Dishes", Assignees: []string{sam, jo, alex}, StartsAt: ptr(at("2026-10-19 19:00")), Repeat: Repeat{Daily: true}})
	require.NoError(t, err)
	assert.Equal(t, sam, dishes.TurnOf())
	assert.Equal(t, jo, dishes.NextTurnOf())
	assert.True(t, dishes.Yours(sam))
	assert.False(t, dishes.Yours(jo))

	mine, err := h.ListItems(sam, ItemFilter{MineOnly: true})
	require.NoError(t, err)
	require.Len(t, mine, 1)
	u, err := h.Updates(sam)
	require.NoError(t, err)
	assert.Equal(t, 1, u.ItemsDue)
	u, err = h.Updates(jo)
	require.NoError(t, err)
	assert.Zero(t, u.ItemsDue)

	now = at("2026-10-19 20:30")
	done, err := h.CompleteItem(sam, dishes.ID)
	require.NoError(t, err)
	assert.Equal(t, jo, done.TurnOf(), "the turn passes on")
	assert.Equal(t, alex, done.NextTurnOf())
	assert.Equal(t, sam, done.DoneBy)
	assert.Equal(t, at("2026-10-20 19:00"), done.StartsAt.In(chicago), "tomorrow at 7")
	assert.False(t, done.Done, "a repeating item is never done")

	_, err = h.CompleteItem("stranger", dishes.ID)
	assert.ErrorIs(t, err, ErrItemNotFound)

	// Jo forgets Tuesday and does them Thursday morning: Thursday's are done
	// and the next night is Friday.
	now = at("2026-10-22 08:00")
	done, err = h.CompleteItem(jo, dishes.ID)
	require.NoError(t, err)
	assert.Equal(t, alex, done.TurnOf())
	assert.Equal(t, at("2026-10-23 19:00"), done.StartsAt.In(chicago))
	assert.Equal(t, sam, done.NextTurnOf(), "the order wraps around")
}

func TestOnlyAssigneesComplete(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)
	pickup, err := h.SaveItem(alex, "", ItemInput{Kind: Pickup, Title: "School pickup", Assignees: []string{sam}, StartsAt: ptr(at("2026-10-22 15:30")), Place: "School"})
	require.NoError(t, err)
	_, err = h.CompleteItem(alex, pickup.ID)
	assert.ErrorIs(t, err, ErrNotYours, "Alex set it up but Sam is doing it")
	done, err := h.CompleteItem(sam, pickup.ID)
	require.NoError(t, err)
	assert.True(t, done.Done)

	open, err := h.ListItems(alex, ItemFilter{})
	require.NoError(t, err)
	assert.Empty(t, open)
	all, err := h.ListItems(alex, ItemFilter{IncludeDone: true})
	require.NoError(t, err)
	assert.Len(t, all, 1)

	// An everyone chore with no one assigned: anyone may tick it.
	bins, err := h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "Bins", Everyone: true})
	require.NoError(t, err)
	_, err = h.CompleteItem(jo, bins.ID)
	require.NoError(t, err)
}

func TestWeeklyRepeatAndDefaultStart(t *testing.T) {
	now := at("2026-10-19 09:00") // Monday
	h := familyAt(t, &now)
	// Bins on Tuesdays and Fridays, no time given: starts Tuesday.
	bins, err := h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "Bins", Assignees: []string{alex}, Repeat: Repeat{Weekdays: []int{5, 2, 2}}})
	require.NoError(t, err)
	assert.Equal(t, []int{2, 5}, bins.Repeat.Weekdays)
	assert.Equal(t, at("2026-10-20 00:00"), bins.StartsAt.In(chicago))

	// Done early on Monday: moves past Tuesday to Friday.
	done, err := h.CompleteItem(alex, bins.ID)
	require.NoError(t, err)
	assert.Equal(t, at("2026-10-23 00:00"), done.StartsAt.In(chicago))
	done, err = h.CompleteItem(alex, bins.ID)
	require.NoError(t, err)
	assert.Equal(t, at("2026-10-27 00:00"), done.StartsAt.In(chicago))
}

func TestNextOccurrenceKeepsWallClockAcrossDST(t *testing.T) {
	// US daylight saving ends Sunday 2026-11-01.
	start := at("2026-10-31 19:00")
	next := nextOccurrence(start, Repeat{Daily: true}, at("2026-10-31 20:00"), chicago)
	assert.Equal(t, "2026-11-01 19:00", next.In(chicago).Format("2006-01-02 15:04"))
}

func TestChangingAssigneesKeepsTheTurn(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)
	in := ItemInput{Kind: Turn, Title: "Dishes", Assignees: []string{sam, jo}}
	dishes, err := h.SaveItem(alex, "", in)
	require.NoError(t, err)
	dishes, err = h.CompleteItem(sam, dishes.ID)
	require.NoError(t, err)
	require.Equal(t, jo, dishes.TurnOf())
	in.Assignees = []string{alex, sam, jo}
	dishes, err = h.SaveItem(alex, dishes.ID, in)
	require.NoError(t, err)
	assert.Equal(t, jo, dishes.TurnOf(), "still Jo's turn")
}

func TestOpenItemLimit(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)
	h.mu.Lock()
	require.NoError(t, h.loadItemsLocked())
	for i := 0; i < MaxOpenItems; i++ {
		h.items = append(h.items, Item{ID: "x", Kind: Chore, Title: "x", CreatedBy: alex})
	}
	h.mu.Unlock()
	_, err := h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "one more"})
	assert.ErrorIs(t, err, ErrTooManyItems)
}

func TestDoneItemsArePrunedAfterAMonth(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)
	it, err := h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "Call the bank"})
	require.NoError(t, err)
	_, err = h.CompleteItem(alex, it.ID)
	require.NoError(t, err)
	now = now.Add(31 * 24 * time.Hour)
	_, err = h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "Something else"})
	require.NoError(t, err)
	all, err := h.ListItems(alex, ItemFilter{IncludeDone: true})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "Something else", all[0].Title)
}

func TestItemsPersistOwnerOnly(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)
	_, err := h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "Bins"})
	require.NoError(t, err)
	info, err := os.Stat(h.itemsPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Equal(t, "items.json", filepath.Base(h.itemsPath))

	again := New(h.path, h.people, h.members)
	items, err := again.ListItems(alex, ItemFilter{})
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

func TestListItemsTimeRangeAndOrder(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)
	for _, in := range []ItemInput{
		{Kind: Chore, Title: "Untimed"},
		{Kind: Pickup, Title: "Thursday", StartsAt: ptr(at("2026-10-22 15:30"))},
		{Kind: Pickup, Title: "Tuesday", StartsAt: ptr(at("2026-10-20 15:30"))},
		{Kind: Pickup, Title: "Next week", StartsAt: ptr(at("2026-10-27 15:30"))},
	} {
		_, err := h.SaveItem(alex, "", in)
		require.NoError(t, err)
	}
	items, err := h.ListItems(alex, ItemFilter{From: at("2026-10-19 00:00"), To: at("2026-10-26 00:00")})
	require.NoError(t, err)
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Title)
	}
	assert.Equal(t, []string{"Tuesday", "Thursday", "Untimed"}, titles)
}

func TestItemsConcurrentSaves(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "x"})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	items, err := h.ListItems(alex, ItemFilter{})
	require.NoError(t, err)
	assert.Len(t, items, 20)
}

func TestItemTools(t *testing.T) {
	now := at("2026-10-19 09:00")
	h := familyAt(t, &now)
	dishes, err := h.SaveItem(alex, "", ItemInput{Kind: Turn, Title: "Dishes", Assignees: []string{sam, jo}, StartsAt: ptr(at("2026-10-19 19:00")), Repeat: Repeat{Daily: true}})
	require.NoError(t, err)
	_, err = h.SaveItem(alex, "", ItemInput{Kind: Chore, Title: "Call the bank"})
	require.NoError(t, err)

	list, doneTool := Tools(h)[2], Tools(h)[3]
	assert.Equal(t, ItemsToolName, list.Name())
	assert.Equal(t, ItemDoneToolName, doneTool.Name())

	joCtx := types.ContextWithUserID(context.Background(), jo)
	res, err := list.Execute(joCtx, map[string]interface{}{})
	require.NoError(t, err)
	require.True(t, res.Success)
	items := res.Data.(map[string]interface{})["items"].([]interface{})
	require.Len(t, items, 1, "Jo sees the dishes, not Alex's own chore")
	first := items[0].(map[string]interface{})
	assert.Equal(t, "Sam", first["turn"])
	assert.Equal(t, "you", first["next_turn"])
	assert.Equal(t, false, first["yours"])
	assert.Equal(t, "Mon 2026-10-19 19:00", first["when"])
	assert.Equal(t, "every day", first["repeats"])

	// Alex set the dishes up but is not taking turns: not Alex's to mark.
	alexCtx := types.ContextWithUserID(context.Background(), alex)
	res, err = doneTool.Execute(alexCtx, map[string]interface{}{"item_id": dishes.ID})
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Equal(t, "NOT_YOURS", res.Error.Code)

	samCtx := types.ContextWithUserID(context.Background(), sam)
	res, err = doneTool.Execute(samCtx, map[string]interface{}{"item_id": dishes.ID})
	require.NoError(t, err)
	require.True(t, res.Success)
	item := res.Data.(map[string]interface{})["item"].(map[string]interface{})
	assert.Equal(t, "jo-3", item["turn"], "Jo has no name yet")
	assert.Equal(t, "you", item["last_done_by"])

	res, err = doneTool.Execute(samCtx, map[string]interface{}{"item_id": "item_nope"})
	require.NoError(t, err)
	assert.Equal(t, "NOT_FOUND", res.Error.Code)
}
