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

// The household (proto/loom/v1/household.proto): people's names and the
// notes they send each other. Every RPC acts as the caller (appOwner), so a
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
	case errors.Is(err, household.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, household.ErrUnknownPerson), errors.Is(err, household.ErrSelf), errors.Is(err, household.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, household.ErrNotAnswerable), errors.Is(err, household.ErrAlreadyAnswered):
		return status.Error(codes.FailedPrecondition, err.Error())
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
