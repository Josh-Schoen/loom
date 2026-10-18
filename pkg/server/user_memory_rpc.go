// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/memory"
	"github.com/teradata-labs/loom/pkg/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// What an agent remembers about the caller (proto/loom/v1/user_memory.proto).
// The graph memory store scopes every read and write to the user ID in ctx,
// so these RPCs only ever see the caller's own memories.

const (
	defaultUserMemoryLimit = 100
	maxUserMemoryLimit     = 500
	maxUserMemoryChars     = 500
	// sourceUser marks memories the person added or corrected themselves.
	sourceUser = "user"
	// userMemorySalience ranks what the person stated directly above routine
	// facts extracted from conversation (default 0.5).
	userMemorySalience = 0.8
)

// userMemoryStore resolves the agent (ID or name) to its graph memory store
// and the agent name memories are keyed by.
func (s *MultiAgentServer) userMemoryStore(agentID string) (memory.GraphMemoryStore, string, error) {
	if agentID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "agent_id is required")
	}
	ag, _, err := s.getAgent(agentID)
	if err != nil {
		return nil, "", err
	}
	store, enabled := ag.GraphMemory()
	if store == nil || !enabled {
		return nil, "", status.Error(codes.FailedPrecondition, "graph memory is not enabled for this agent")
	}
	return store, ag.GetName(), nil
}

// userMemoryContent validates the text of an added or corrected memory.
func userMemoryContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", status.Error(codes.InvalidArgument, "content is required")
	}
	if utf8.RuneCountInString(content) > maxUserMemoryChars {
		return "", status.Errorf(codes.InvalidArgument, "content is longer than %d characters", maxUserMemoryChars)
	}
	return content, nil
}

// activeUserMemory returns the caller's memory if it belongs to agentName and
// is still recalled; NOT_FOUND otherwise (including another user's memory).
func activeUserMemory(ctx context.Context, store memory.GraphMemoryStore, agentName, memoryID string) (*memory.Memory, error) {
	if memoryID == "" {
		return nil, status.Error(codes.InvalidArgument, "memory_id is required")
	}
	mem, err := store.GetMemory(ctx, agentName, memoryID)
	if errors.Is(err, memory.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "memory not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load memory: %v", err)
	}
	if mem.IsSuperseded || mem.ExpiresAt != nil {
		return nil, status.Error(codes.NotFound, "memory not found")
	}
	return mem, nil
}

func toUserMemory(m *memory.Memory) *loomv1.UserMemory {
	return &loomv1.UserMemory{
		Id:         m.ID,
		Content:    m.Content,
		Summary:    m.Summary,
		MemoryType: m.MemoryType,
		Source:     m.Source,
		CreatedAt:  m.CreatedAt.Unix(),
		EventDate:  m.EventDate,
	}
}

// ListUserMemories lists the caller's active memories for an agent.
func (s *MultiAgentServer) ListUserMemories(ctx context.Context, req *loomv1.ListUserMemoriesRequest) (*loomv1.ListUserMemoriesResponse, error) {
	store, agentName, err := s.userMemoryStore(req.GetAgentId())
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultUserMemoryLimit
	}
	if limit > maxUserMemoryLimit {
		limit = maxUserMemoryLimit
	}
	offset := int(req.GetOffset())
	if offset < 0 {
		return nil, status.Error(codes.InvalidArgument, "offset must not be negative")
	}

	mems, total, err := store.ListMemories(ctx, agentName, limit, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list memories: %v", err)
	}
	resp := &loomv1.ListUserMemoriesResponse{
		Memories:   make([]*loomv1.UserMemory, 0, len(mems)),
		TotalCount: types.SafeInt32(total),
	}
	for _, m := range mems {
		resp.Memories = append(resp.Memories, toUserMemory(m))
	}
	return resp, nil
}

// AddUserMemory stores a fact the caller wants the agent to remember.
func (s *MultiAgentServer) AddUserMemory(ctx context.Context, req *loomv1.AddUserMemoryRequest) (*loomv1.UserMemory, error) {
	store, agentName, err := s.userMemoryStore(req.GetAgentId())
	if err != nil {
		return nil, err
	}
	content, err := userMemoryContent(req.GetContent())
	if err != nil {
		return nil, err
	}
	mem, err := store.Remember(ctx, &memory.Memory{
		AgentID:       agentName,
		MemoryAgentID: agentName,
		Content:       content,
		MemoryType:    memory.MemoryTypeFact,
		Source:        sourceUser,
		Salience:      userMemorySalience,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "remember: %v", err)
	}
	return toUserMemory(mem), nil
}

// CorrectUserMemory supersedes one of the caller's memories with new text.
func (s *MultiAgentServer) CorrectUserMemory(ctx context.Context, req *loomv1.CorrectUserMemoryRequest) (*loomv1.UserMemory, error) {
	store, agentName, err := s.userMemoryStore(req.GetAgentId())
	if err != nil {
		return nil, err
	}
	content, err := userMemoryContent(req.GetContent())
	if err != nil {
		return nil, err
	}
	old, err := activeUserMemory(ctx, store, agentName, req.GetMemoryId())
	if err != nil {
		return nil, err
	}
	mem, err := store.Supersede(ctx, old.ID, &memory.Memory{
		AgentID:       agentName,
		MemoryAgentID: agentName,
		Content:       content,
		MemoryType:    old.MemoryType,
		Source:        sourceUser,
		Salience:      max(old.Salience, userMemorySalience),
		EntityIDs:     old.EntityIDs,
	})
	if errors.Is(err, memory.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "memory not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "supersede: %v", err)
	}
	return toUserMemory(mem), nil
}

// ForgetUserMemory forgets one of the caller's memories.
func (s *MultiAgentServer) ForgetUserMemory(ctx context.Context, req *loomv1.ForgetUserMemoryRequest) (*loomv1.ForgetUserMemoryResponse, error) {
	store, agentName, err := s.userMemoryStore(req.GetAgentId())
	if err != nil {
		return nil, err
	}
	old, err := activeUserMemory(ctx, store, agentName, req.GetMemoryId())
	if err != nil {
		return nil, err
	}
	err = store.Forget(ctx, old.ID)
	if errors.Is(err, memory.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "memory not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "forget: %v", err)
	}
	return &loomv1.ForgetUserMemoryResponse{}, nil
}

// ForgetAllUserMemories forgets everything the agent remembers about the caller.
func (s *MultiAgentServer) ForgetAllUserMemories(ctx context.Context, req *loomv1.ForgetAllUserMemoriesRequest) (*loomv1.ForgetAllUserMemoriesResponse, error) {
	store, agentName, err := s.userMemoryStore(req.GetAgentId())
	if err != nil {
		return nil, err
	}
	n, err := store.ForgetAll(ctx, agentName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "forget all: %v", err)
	}
	return &loomv1.ForgetAllUserMemoriesResponse{
		ForgottenCount: types.SafeInt32(n),
	}, nil
}
