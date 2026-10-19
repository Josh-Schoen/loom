// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/household"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// newHouseholdServer is a server with apps and a household of Alex, Sam and
// Jo (Jo has a paired phone and no name yet).
func newHouseholdServer(t *testing.T) *MultiAgentServer {
	t.Helper()
	dir := t.TempDir()
	srv, _ := newChecklistServer(t, filepath.Join(dir, "apps"))
	peoplePath := filepath.Join(dir, household.PeopleFileName)
	require.NoError(t, os.WriteFile(peoplePath, []byte(`{"alex": "Alex", "sam": "Sam"}`), 0o600))
	srv.SetHousehold(household.New(filepath.Join(dir, "household", "notes.json"), household.NewPeople(peoplePath), staticMembers{"jo"}))
	return srv
}

type staticMembers []string

func (s staticMembers) UserIDs() []string { return s }

func TestHouseholdOffIsFailedPrecondition(t *testing.T) {
	srv, _ := newChecklistServer(t, t.TempDir())
	_, err := srv.ListHouseholdMembers(ctxFor("alex"), &loomv1.ListHouseholdMembersRequest{})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	_, err = srv.GetHouseholdUpdates(ctxFor("alex"), &loomv1.GetHouseholdUpdatesRequest{})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestListHouseholdMembers(t *testing.T) {
	srv := newHouseholdServer(t)
	resp, err := srv.ListHouseholdMembers(ctxFor("sam"), &loomv1.ListHouseholdMembersRequest{})
	require.NoError(t, err)
	var got []string
	for _, m := range resp.GetMembers() {
		label := m.GetName()
		if m.GetIsYou() {
			label += " (you)"
		}
		got = append(got, label)
	}
	assert.Equal(t, []string{"Alex", "jo", "Sam (you)"}, got)
}

// Alex asks Sam about pickup; Sam sees it and answers; Alex sees the answer.
// Jo sees none of it.
func TestHouseholdAskAndAnswerAcrossPeople(t *testing.T) {
	srv := newHouseholdServer(t)
	alex, sam, jo := ctxFor("alex"), ctxFor("sam"), ctxFor("jo")

	sent, err := srv.SendHouseholdNote(alex, &loomv1.SendHouseholdNoteRequest{
		ToUserId: "sam", Kind: loomv1.HouseholdNoteKind_HOUSEHOLD_NOTE_KIND_ASK, Text: "Can you do pickup Thursday?",
	})
	require.NoError(t, err)
	assert.Equal(t, "Alex", sent.GetFromName())
	assert.Equal(t, "Sam", sent.GetToName())
	assert.True(t, sent.GetFromYou())

	updates, err := srv.GetHouseholdUpdates(sam, &loomv1.GetHouseholdUpdatesRequest{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, updates.GetUnreadNotes())
	assert.EqualValues(t, 1, updates.GetUnansweredAsks())
	assert.NotNil(t, updates.GetUpdatedAt())

	samNotes, err := srv.ListHouseholdNotes(sam, &loomv1.ListHouseholdNotesRequest{})
	require.NoError(t, err)
	require.Len(t, samNotes.GetNotes(), 1)
	assert.False(t, samNotes.GetNotes()[0].GetFromYou())

	joNotes, err := srv.ListHouseholdNotes(jo, &loomv1.ListHouseholdNotesRequest{})
	require.NoError(t, err)
	assert.Empty(t, joNotes.GetNotes(), "Jo sees no one else's notes")
	_, err = srv.AnswerHouseholdNote(jo, &loomv1.AnswerHouseholdNoteRequest{NoteId: sent.GetId(), Text: "no"})
	assert.Equal(t, codes.NotFound, status.Code(err), "Jo cannot answer for Sam")
	_, err = srv.AnswerHouseholdNote(alex, &loomv1.AnswerHouseholdNoteRequest{NoteId: sent.GetId(), Text: "yes"})
	assert.Equal(t, codes.NotFound, status.Code(err), "the asker cannot answer their own ask")

	answered, err := srv.AnswerHouseholdNote(sam, &loomv1.AnswerHouseholdNoteRequest{NoteId: sent.GetId(), Text: "Yes, I'll get them at 3:30"})
	require.NoError(t, err)
	assert.Equal(t, "Yes, I'll get them at 3:30", answered.GetAnswer())
	_, err = srv.AnswerHouseholdNote(sam, &loomv1.AnswerHouseholdNoteRequest{NoteId: sent.GetId(), Text: "no"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	updates, err = srv.GetHouseholdUpdates(alex, &loomv1.GetHouseholdUpdatesRequest{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, updates.GetNewAnswers())

	marked, err := srv.MarkHouseholdNotesRead(alex, &loomv1.MarkHouseholdNotesReadRequest{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, marked.GetMarked())
	updates, err = srv.GetHouseholdUpdates(alex, &loomv1.GetHouseholdUpdatesRequest{})
	require.NoError(t, err)
	assert.Zero(t, updates.GetNewAnswers())
}

func TestSendHouseholdNoteRejects(t *testing.T) {
	srv := newHouseholdServer(t)
	alex := ctxFor("alex")
	tell := loomv1.HouseholdNoteKind_HOUSEHOLD_NOTE_KIND_TELL
	cases := map[string]*loomv1.SendHouseholdNoteRequest{
		"no recipient": {Kind: tell, Text: "hi"},
		"stranger":     {ToUserId: "stranger", Kind: tell, Text: "hi"},
		"self":         {ToUserId: "alex", Kind: tell, Text: "hi"},
		"no kind":      {ToUserId: "sam", Text: "hi"},
		"empty":        {ToUserId: "sam", Kind: tell, Text: "  "},
	}
	for name, req := range cases {
		_, err := srv.SendHouseholdNote(alex, req)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}
	// Someone outside the household cannot send.
	_, err := srv.SendHouseholdNote(ctxFor("stranger"), &loomv1.SendHouseholdNoteRequest{ToUserId: "sam", Kind: tell, Text: "hi"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	note, err := srv.SendHouseholdNote(alex, &loomv1.SendHouseholdNoteRequest{ToUserId: "sam", Kind: tell, Text: "Running late"})
	require.NoError(t, err)
	_, err = srv.AnswerHouseholdNote(ctxFor("sam"), &loomv1.AnswerHouseholdNoteRequest{NoteId: note.GetId(), Text: "ok"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "a tell has no answer")
}

func TestSharedAppNamesWhoSharedIt(t *testing.T) {
	srv := newHouseholdServer(t)
	created, err := srv.CreateUIApp(ctxFor("alex"), &loomv1.CreateUIAppRequest{Name: "groceries", Spec: groceriesSpec(t), Shared: true})
	require.NoError(t, err)
	assert.Equal(t, "alex", created.GetApp().GetSharedBy())
	assert.Equal(t, "Alex", created.GetApp().GetSharedByName())

	// Without a name, shared_by_name stays empty.
	created, err = srv.CreateUIApp(ctxFor("jo"), &loomv1.CreateUIAppRequest{Name: "chores", Spec: groceriesSpec(t), Shared: true})
	require.NoError(t, err)
	assert.Empty(t, created.GetApp().GetSharedByName())
}

func TestDeviceServerUserIDsSkipsRevoked(t *testing.T) {
	s := newTestDeviceServer(t, true)
	for _, user := range []string{"alex", "sam", "alex"} {
		created, err := s.CreatePairingCode(context.Background(), &loomv1.CreatePairingCodeRequest{UserId: user})
		require.NoError(t, err)
		_, err = s.RedeemPairingCode(context.Background(), &loomv1.RedeemPairingCodeRequest{Code: created.GetCode(), DeviceName: "Phone"})
		require.NoError(t, err)
	}
	assert.ElementsMatch(t, []string{"alex", "sam"}, s.UserIDs())

	for _, d := range s.manager.List() {
		if d.UserID == "sam" {
			_, err := s.manager.Revoke(d.ID)
			require.NoError(t, err)
		}
	}
	assert.Equal(t, []string{"alex"}, s.UserIDs())
}

// Alex sets up dishes taking turns between Sam and Jo, a dentist for Jo,
// and a chore of Alex's own. Each sees only what involves them; the turn
// passes on when Sam ticks it.
func TestHouseholdItemsAcrossPeople(t *testing.T) {
	srv := newHouseholdServer(t)
	alex, sam, jo := ctxFor("alex"), ctxFor("sam"), ctxFor("jo")
	at := timestamppb.New(time.Now().Add(-time.Hour))

	dishes, err := srv.SaveHouseholdItem(alex, &loomv1.SaveHouseholdItemRequest{Item: &loomv1.HouseholdItem{
		Kind: loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_TURN, Title: "Dishes",
		Assignees: []string{"sam", "jo"}, StartsAt: at, Repeat: &loomv1.HouseholdRepeat{Daily: true},
	}})
	require.NoError(t, err)
	assert.Equal(t, []string{"Sam", "jo"}, dishes.GetAssigneeNames())
	assert.Equal(t, "Sam", dishes.GetTurnName())
	assert.Equal(t, "jo", dishes.GetNextTurnUserId())
	assert.Equal(t, "Alex", dishes.GetCreatedByName())
	assert.False(t, dishes.GetYours(), "Alex is not taking turns")
	assert.True(t, dishes.GetCanEdit())

	_, err = srv.SaveHouseholdItem(alex, &loomv1.SaveHouseholdItemRequest{Item: &loomv1.HouseholdItem{
		Kind: loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_APPOINTMENT, Title: "Dentist", ForUserId: "jo",
		Assignees: []string{"alex"}, StartsAt: timestamppb.New(time.Now().Add(48 * time.Hour)), Place: "Main St",
	}})
	require.NoError(t, err)
	_, err = srv.SaveHouseholdItem(alex, &loomv1.SaveHouseholdItemRequest{Item: &loomv1.HouseholdItem{
		Kind: loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_CHORE, Title: "Call the bank",
	}})
	require.NoError(t, err)

	titles := func(ctx context.Context, mine bool) []string {
		resp, err := srv.ListHouseholdItems(ctx, &loomv1.ListHouseholdItemsRequest{MineOnly: mine})
		require.NoError(t, err)
		var out []string
		for _, it := range resp.GetItems() {
			out = append(out, it.GetTitle())
		}
		return out
	}
	assert.ElementsMatch(t, []string{"Dishes", "Dentist", "Call the bank"}, titles(alex, false))
	assert.ElementsMatch(t, []string{"Dishes"}, titles(sam, false))
	assert.ElementsMatch(t, []string{"Dishes", "Dentist"}, titles(jo, false), "Jo sees only what involves Jo")
	assert.Equal(t, []string{"Dishes"}, titles(sam, true))
	assert.Empty(t, titles(jo, true), "not Jo's turn yet")

	updates, err := srv.GetHouseholdUpdates(sam, &loomv1.GetHouseholdUpdatesRequest{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, updates.GetItemsDue())

	_, err = srv.CompleteHouseholdItem(alex, &loomv1.CompleteHouseholdItemRequest{ItemId: dishes.GetId()})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "Alex is not taking turns")
	done, err := srv.CompleteHouseholdItem(sam, &loomv1.CompleteHouseholdItemRequest{ItemId: dishes.GetId()})
	require.NoError(t, err)
	assert.Equal(t, "jo", done.GetTurnUserId())
	assert.Equal(t, "Sam", done.GetDoneByName())
	assert.True(t, done.GetStartsAt().AsTime().After(time.Now()), "moved to its next date")
	assert.Equal(t, []string{"Dishes"}, titles(jo, true), "now Jo's turn")

	_, err = srv.DeleteHouseholdItem(sam, &loomv1.DeleteHouseholdItemRequest{ItemId: "item_nope"})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = srv.SaveHouseholdItem(sam, &loomv1.SaveHouseholdItemRequest{Item: &loomv1.HouseholdItem{
		Id: dishes.GetId(), Kind: loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_TURN, Title: "Dishes", Assignees: []string{"sam", "jo"},
	}})
	require.NoError(t, err, "Sam is involved, so may change it")
	_, err = srv.DeleteHouseholdItem(jo, &loomv1.DeleteHouseholdItemRequest{ItemId: dishes.GetId()})
	require.NoError(t, err)
	assert.Empty(t, titles(sam, false))
}

func TestHouseholdEveryoneItemIsSeenButNotEditable(t *testing.T) {
	srv := newHouseholdServer(t)
	bins, err := srv.SaveHouseholdItem(ctxFor("alex"), &loomv1.SaveHouseholdItemRequest{Item: &loomv1.HouseholdItem{
		Kind: loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_CHORE, Title: "Bins", Everyone: true,
		Repeat: &loomv1.HouseholdRepeat{Weekdays: []int32{2}},
	}})
	require.NoError(t, err)
	assert.Equal(t, []int32{2}, bins.GetRepeat().GetWeekdays())
	assert.NotNil(t, bins.GetStartsAt(), "starts on its next Tuesday")

	resp, err := srv.ListHouseholdItems(ctxFor("jo"), &loomv1.ListHouseholdItemsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.GetItems(), 1)
	assert.True(t, resp.GetItems()[0].GetYours(), "no one assigned: anyone's to do")
	assert.False(t, resp.GetItems()[0].GetCanEdit())
	_, err = srv.DeleteHouseholdItem(ctxFor("jo"), &loomv1.DeleteHouseholdItemRequest{ItemId: bins.GetId()})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = srv.CompleteHouseholdItem(ctxFor("jo"), &loomv1.CompleteHouseholdItemRequest{ItemId: bins.GetId()})
	require.NoError(t, err)
}

func TestSaveHouseholdItemRejects(t *testing.T) {
	srv := newHouseholdServer(t)
	alex := ctxFor("alex")
	chore := loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_CHORE
	cases := map[string]*loomv1.SaveHouseholdItemRequest{
		"no item":     {},
		"no kind":     {Item: &loomv1.HouseholdItem{Title: "x"}},
		"no title":    {Item: &loomv1.HouseholdItem{Kind: chore}},
		"stranger":    {Item: &loomv1.HouseholdItem{Kind: chore, Title: "x", Assignees: []string{"stranger"}}},
		"turn of one": {Item: &loomv1.HouseholdItem{Kind: loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_TURN, Title: "x", Assignees: []string{"sam"}}},
		"bad weekday": {Item: &loomv1.HouseholdItem{Kind: chore, Title: "x", Repeat: &loomv1.HouseholdRepeat{Weekdays: []int32{0}}}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := srv.SaveHouseholdItem(alex, req)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func TestHouseholdItemRPCsAreDeviceAllowed(t *testing.T) {
	for _, m := range []string{"ListHouseholdItems", "SaveHouseholdItem", "CompleteHouseholdItem", "DeleteHouseholdItem"} {
		assert.True(t, deviceAllowedMethods["/loom.v1.LoomService/"+m], m)
	}
}
