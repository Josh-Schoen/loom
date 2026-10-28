// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWeaveContentBlocks(t *testing.T) {
	t.Run("no images", func(t *testing.T) {
		blocks, err := weaveContentBlocks(&loomv1.WeaveRequest{Query: "hi"})
		require.NoError(t, err)
		assert.Nil(t, blocks)
	})

	t.Run("query first, then each picture", func(t *testing.T) {
		blocks, err := weaveContentBlocks(&loomv1.WeaveRequest{Query: "what is this?", Images: []*loomv1.WeaveImage{
			{MediaType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}},
			{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
		}})
		require.NoError(t, err)
		require.Len(t, blocks, 3)
		assert.Equal(t, "text", blocks[0].Type)
		assert.Equal(t, "what is this?", blocks[0].Text)
		assert.Equal(t, "image", blocks[1].Type)
		assert.Equal(t, "base64", blocks[1].Image.Source.Type)
		assert.Equal(t, "image/jpeg", blocks[1].Image.Source.MediaType)
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff}), blocks[1].Image.Source.Data)
		assert.Equal(t, "image/png", blocks[2].Image.Source.MediaType)
	})

	for name, req := range map[string]*loomv1.WeaveRequest{
		"unknown type": {Query: "q", Images: []*loomv1.WeaveImage{{MediaType: "image/heic", Data: []byte{1}}}},
		"empty":        {Query: "q", Images: []*loomv1.WeaveImage{{MediaType: "image/jpeg"}}},
		"too large":    {Query: "q", Images: []*loomv1.WeaveImage{{MediaType: "image/jpeg", Data: make([]byte, maxWeaveImageBytes+1)}}},
		"too many":     {Query: "q", Images: make([]*loomv1.WeaveImage, maxWeaveImages+1)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := weaveContentBlocks(req)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// seeingLLM records the messages it was sent.
type seeingLLM struct {
	mu       sync.Mutex
	messages []llmtypes.Message
}

func (m *seeingLLM) Chat(_ context.Context, messages []llmtypes.Message, _ []shuttle.Tool) (*llmtypes.LLMResponse, error) {
	m.mu.Lock()
	m.messages = append([]llmtypes.Message(nil), messages...)
	m.mu.Unlock()
	return &llmtypes.LLMResponse{Content: "A lake at sunset."}, nil
}

func (m *seeingLLM) Name() string  { return "mock" }
func (m *seeingLLM) Model() string { return "mock-model" }

// TestWeave_ImagesReachTheModel checks the pictures in a WeaveRequest are in
// the user turn the provider is sent, after the query.
func TestWeave_ImagesReachTheModel(t *testing.T) {
	llm := &seeingLLM{}
	ag := agent.NewAgent(&mockBackend{}, llm)
	srv := NewMultiAgentServer(map[string]*agent.Agent{"agent1": ag}, nil)

	resp, err := srv.Weave(context.Background(), &loomv1.WeaveRequest{
		Query:  "Where was this taken?",
		Images: []*loomv1.WeaveImage{{MediaType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}}},
	})
	require.NoError(t, err)
	assert.Equal(t, "A lake at sunset.", resp.GetText())

	llm.mu.Lock()
	defer llm.mu.Unlock()
	var user *llmtypes.Message
	for i := range llm.messages {
		if llm.messages[i].Role == "user" {
			user = &llm.messages[i]
		}
	}
	require.NotNil(t, user, "the provider was sent the user turn")
	var text, images []string
	for _, b := range user.ContentBlocks {
		switch b.Type {
		case "text":
			text = append(text, b.Text)
		case "image":
			images = append(images, b.Image.Source.MediaType+" "+b.Image.Source.Data)
		}
	}
	require.NotEmpty(t, text)
	assert.Contains(t, strings.Join(text, " "), "Where was this taken?")
	assert.Equal(t, []string{"image/jpeg /9j/"}, images)
}

// TestLimitBodies checks weaves get room for pictures and nothing else does.
func TestLimitBodies(t *testing.T) {
	read := limitBodies(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
	}))
	body := strings.Repeat("a", 6<<20)
	for path, want := range map[string]int{"/v1/weave:stream": http.StatusOK, "/v1/weave": http.StatusOK, "/v1/sessions": http.StatusRequestEntityTooLarge} {
		w := httptest.NewRecorder()
		read.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		assert.Equal(t, want, w.Code, path)
	}
}
