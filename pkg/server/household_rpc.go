// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"errors"
	"time"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/household"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The household (proto/loom/v1/household.proto): people's names, the
// notes they send each other, and who's doing what (items). Every RPC acts as the caller (appOwner), so a
// person sees only the notes they sent or received.

const (
	defaultHouseholdNotesLimit = 50
	maxHouseholdNotesLimit     = 200
)

// SetHousehold turns on the household RPCs (server.household.enabled).
// This should be called after NewMultiAgentServer(), before serving.
func (s *MultiAgentServer) SetHousehold(h *household.Household) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.household = h
}

func (s *MultiAgentServer) householdOrNil() *household.Household {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.household
}

func (s *MultiAgentServer) householdFor() (*household.Household, error) {
	h := s.householdOrNil()
	if h == nil {
		return nil, status.Error(codes.FailedPrecondition, "the household is not enabled on this server (server.household.enabled)")
	}
	return h, nil
}

// householdError maps store errors to gRPC codes.
func householdError(err error) error {
	switch {
	case errors.Is(err, household.ErrNotFound), errors.Is(err, household.ErrItemNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, household.ErrUnknownPerson), errors.Is(err, household.ErrSelf), errors.Is(err, household.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, household.ErrNotAnswerable), errors.Is(err, household.ErrAlreadyAnswered):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, household.ErrNotInvolved), errors.Is(err, household.ErrNotYours):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, household.ErrTooManyItems):
		return status.Error(codes.ResourceExhausted, err.Error())
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}

// ListHouseholdMembers lists the people in the household, by name.
func (s *MultiAgentServer) ListHouseholdMembers(ctx context.Context, _ *loomv1.ListHouseholdMembersRequest) (*loomv1.ListHouseholdMembersResponse, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	members, err := h.MemberList()
	if err != nil {
		return nil, householdError(err)
	}
	me := appOwner(ctx)
	resp := &loomv1.ListHouseholdMembersResponse{}
	for _, m := range members {
		resp.Members = append(resp.Members, &loomv1.HouseholdMember{UserId: m.UserID, Name: m.Name, IsYou: m.UserID == me})
	}
	return resp, nil
}

// SendHouseholdNote sends a note from the caller to another member.
func (s *MultiAgentServer) SendHouseholdNote(ctx context.Context, req *loomv1.SendHouseholdNoteRequest) (*loomv1.HouseholdNote, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	var kind household.Kind
	switch req.GetKind() {
	case loomv1.HouseholdNoteKind_HOUSEHOLD_NOTE_KIND_TELL:
		kind = household.Tell
	case loomv1.HouseholdNoteKind_HOUSEHOLD_NOTE_KIND_ASK:
		kind = household.Ask
	default:
		return nil, status.Error(codes.InvalidArgument, "kind must be TELL or ASK")
	}
	if req.GetToUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "to_user_id is required")
	}
	me := appOwner(ctx)
	note, err := h.Send(me, req.GetToUserId(), kind, req.GetText())
	if err != nil {
		return nil, householdError(err)
	}
	return householdNoteToProto(h, me, note), nil
}

// ListHouseholdNotes lists the notes the caller sent or received.
func (s *MultiAgentServer) ListHouseholdNotes(ctx context.Context, req *loomv1.ListHouseholdNotesRequest) (*loomv1.ListHouseholdNotesResponse, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultHouseholdNotesLimit
	}
	if limit > maxHouseholdNotesLimit {
		limit = maxHouseholdNotesLimit
	}
	me := appOwner(ctx)
	notes, err := h.List(me, limit)
	if err != nil {
		return nil, householdError(err)
	}
	resp := &loomv1.ListHouseholdNotesResponse{}
	for _, n := range notes {
		resp.Notes = append(resp.Notes, householdNoteToProto(h, me, n))
	}
	return resp, nil
}

// AnswerHouseholdNote answers an ask sent to the caller.
func (s *MultiAgentServer) AnswerHouseholdNote(ctx context.Context, req *loomv1.AnswerHouseholdNoteRequest) (*loomv1.HouseholdNote, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	if req.GetNoteId() == "" {
		return nil, status.Error(codes.InvalidArgument, "note_id is required")
	}
	me := appOwner(ctx)
	note, err := h.Answer(me, req.GetNoteId(), req.GetText())
	if err != nil {
		return nil, householdError(err)
	}
	return householdNoteToProto(h, me, note), nil
}

// MarkHouseholdNotesRead marks notes to the caller read and answers seen.
func (s *MultiAgentServer) MarkHouseholdNotesRead(ctx context.Context, req *loomv1.MarkHouseholdNotesReadRequest) (*loomv1.MarkHouseholdNotesReadResponse, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	n, err := h.MarkRead(appOwner(ctx), req.GetNoteIds())
	if err != nil {
		return nil, householdError(err)
	}
	return &loomv1.MarkHouseholdNotesReadResponse{Marked: int32(n)}, nil // #nosec G115 -- bounded by notes kept per person
}

// GetHouseholdUpdates returns what is new for the caller.
func (s *MultiAgentServer) GetHouseholdUpdates(ctx context.Context, _ *loomv1.GetHouseholdUpdatesRequest) (*loomv1.HouseholdUpdates, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	u, err := h.Updates(appOwner(ctx))
	if err != nil {
		return nil, householdError(err)
	}
	// Counts are bounded by household.MaxNotesPerPerson.
	resp := &loomv1.HouseholdUpdates{
		UnreadNotes:    int32(u.UnreadNotes),    // #nosec G115
		UnansweredAsks: int32(u.UnansweredAsks), // #nosec G115
		NewAnswers:     int32(u.NewAnswers),     // #nosec G115
		ItemsDue:       int32(u.ItemsDue),       // #nosec G115 -- bounded by household.MaxOpenItems
	}
	if !u.UpdatedAt.IsZero() {
		resp.UpdatedAt = timestamppb.New(u.UpdatedAt)
	}
	return resp, nil
}

func householdNoteToProto(h *household.Household, me string, n household.Note) *loomv1.HouseholdNote {
	ts := func(t *time.Time) *timestamppb.Timestamp {
		if t == nil {
			return nil
		}
		return timestamppb.New(*t)
	}
	kind := loomv1.HouseholdNoteKind_HOUSEHOLD_NOTE_KIND_TELL
	if n.Kind == household.Ask {
		kind = loomv1.HouseholdNoteKind_HOUSEHOLD_NOTE_KIND_ASK
	}
	return &loomv1.HouseholdNote{
		Id:           n.ID,
		FromUserId:   n.From,
		FromName:     h.People().Name(n.From),
		ToUserId:     n.To,
		ToName:       h.People().Name(n.To),
		Kind:         kind,
		Text:         n.Text,
		CreatedAt:    timestamppb.New(n.CreatedAt),
		ReadAt:       ts(n.ReadAt),
		Answer:       n.Answer,
		AnsweredAt:   ts(n.AnsweredAt),
		AnswerSeenAt: ts(n.AnswerSeenAt),
		FromYou:      n.From == me,
	}
}

// ---- Who's doing what ----

// ListHouseholdItems lists the items the caller may see.
func (s *MultiAgentServer) ListHouseholdItems(ctx context.Context, req *loomv1.ListHouseholdItemsRequest) (*loomv1.ListHouseholdItemsResponse, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	f := household.ItemFilter{MineOnly: req.GetMineOnly(), IncludeDone: req.GetIncludeDone()}
	if req.GetFrom() != nil {
		f.From = req.GetFrom().AsTime()
	}
	if req.GetTo() != nil {
		f.To = req.GetTo().AsTime()
	}
	me := appOwner(ctx)
	items, err := h.ListItems(me, f)
	if err != nil {
		return nil, householdError(err)
	}
	resp := &loomv1.ListHouseholdItemsResponse{}
	for _, it := range items {
		resp.Items = append(resp.Items, householdItemToProto(h, me, it))
	}
	return resp, nil
}

// SaveHouseholdItem creates an item or changes one the caller is involved in.
func (s *MultiAgentServer) SaveHouseholdItem(ctx context.Context, req *loomv1.SaveHouseholdItemRequest) (*loomv1.HouseholdItem, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	in := req.GetItem()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "item is required")
	}
	var kind household.ItemKind
	switch in.GetKind() {
	case loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_CHORE:
		kind = household.Chore
	case loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_TURN:
		kind = household.Turn
	case loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_PICKUP:
		kind = household.Pickup
	case loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_APPOINTMENT:
		kind = household.Appointment
	default:
		return nil, status.Error(codes.InvalidArgument, "kind must be CHORE, TURN, PICKUP or APPOINTMENT")
	}
	input := household.ItemInput{
		Kind:      kind,
		Title:     in.GetTitle(),
		Assignees: in.GetAssignees(),
		ForUser:   in.GetForUserId(),
		Everyone:  in.GetEveryone(),
		Place:     in.GetPlace(),
		Notes:     in.GetNotes(),
	}
	if in.GetStartsAt() != nil {
		t := in.GetStartsAt().AsTime()
		input.StartsAt = &t
	}
	if r := in.GetRepeat(); r != nil {
		input.Repeat.Daily = r.GetDaily()
		for _, d := range r.GetWeekdays() {
			input.Repeat.Weekdays = append(input.Repeat.Weekdays, int(d))
		}
	}
	me := appOwner(ctx)
	it, err := h.SaveItem(me, in.GetId(), input)
	if err != nil {
		return nil, householdError(err)
	}
	return householdItemToProto(h, me, it), nil
}

// CompleteHouseholdItem marks an item done by the caller.
func (s *MultiAgentServer) CompleteHouseholdItem(ctx context.Context, req *loomv1.CompleteHouseholdItemRequest) (*loomv1.HouseholdItem, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	if req.GetItemId() == "" {
		return nil, status.Error(codes.InvalidArgument, "item_id is required")
	}
	me := appOwner(ctx)
	it, err := h.CompleteItem(me, req.GetItemId())
	if err != nil {
		return nil, householdError(err)
	}
	return householdItemToProto(h, me, it), nil
}

// DeleteHouseholdItem deletes an item the caller is involved in.
func (s *MultiAgentServer) DeleteHouseholdItem(ctx context.Context, req *loomv1.DeleteHouseholdItemRequest) (*loomv1.DeleteHouseholdItemResponse, error) {
	h, err := s.householdFor()
	if err != nil {
		return nil, err
	}
	if req.GetItemId() == "" {
		return nil, status.Error(codes.InvalidArgument, "item_id is required")
	}
	if err := h.DeleteItem(appOwner(ctx), req.GetItemId()); err != nil {
		return nil, householdError(err)
	}
	return &loomv1.DeleteHouseholdItemResponse{}, nil
}

func householdItemToProto(h *household.Household, me string, it household.Item) *loomv1.HouseholdItem {
	people := h.People()
	kinds := map[household.ItemKind]loomv1.HouseholdItemKind{
		household.Chore:       loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_CHORE,
		household.Turn:        loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_TURN,
		household.Pickup:      loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_PICKUP,
		household.Appointment: loomv1.HouseholdItemKind_HOUSEHOLD_ITEM_KIND_APPOINTMENT,
	}
	out := &loomv1.HouseholdItem{
		Id:            it.ID,
		Kind:          kinds[it.Kind],
		Title:         it.Title,
		Assignees:     append([]string(nil), it.Assignees...),
		ForUserId:     it.ForUser,
		Everyone:      it.Everyone,
		Place:         it.Place,
		Notes:         it.Notes,
		CreatedBy:     it.CreatedBy,
		CreatedByName: people.Name(it.CreatedBy),
		CreatedAt:     timestamppb.New(it.CreatedAt),
		UpdatedAt:     timestamppb.New(it.UpdatedAt),
		DoneBy:        it.DoneBy,
		Done:          it.Done,
		Yours:         it.Yours(me),
		CanEdit:       it.Involves(me),
	}
	for _, a := range it.Assignees {
		out.AssigneeNames = append(out.AssigneeNames, people.Name(a))
	}
	if it.ForUser != "" {
		out.ForName = people.Name(it.ForUser)
	}
	if it.StartsAt != nil {
		out.StartsAt = timestamppb.New(*it.StartsAt)
	}
	if it.Repeat.Repeats() {
		out.Repeat = &loomv1.HouseholdRepeat{Daily: it.Repeat.Daily}
		for _, d := range it.Repeat.Weekdays {
			out.Repeat.Weekdays = append(out.Repeat.Weekdays, int32(d)) // #nosec G115 -- 1..7
		}
	}
	if turn := it.TurnOf(); turn != "" {
		out.TurnUserId, out.TurnName = turn, people.Name(turn)
		next := it.NextTurnOf()
		out.NextTurnUserId, out.NextTurnName = next, people.Name(next)
	}
	if it.DoneAt != nil {
		out.DoneAt = timestamppb.New(*it.DoneAt)
		out.DoneByName = people.Name(it.DoneBy)
	}
	return out
}
