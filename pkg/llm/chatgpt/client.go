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

// Package chatgpt is an LLM provider that answers with a ChatGPT user's
// plan (Sign in with ChatGPT, plan usage) instead of an API key: the user
// signs in with ChatGPT (Auth.SignIn), their tokens are kept in a file
// (Store) and refreshed (TokenSource), and requests go to the public
// Responses API with the user's access token, counted against their plan's
// usage limits rather than billed per token.
//
// OpenAI's terms for plan usage allow requests only for the user who
// signed in, from a runtime that user controls. A Client is therefore bound
// to one user (Config.UserID) and refuses requests made for anyone else
// (ErrOtherUser).
//
// Plan usage is a preview; see docs/reference/llm-chatgpt.md for what it
// does not support.
package chatgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
	"github.com/teradata-labs/loom/pkg/shuttle"
	"github.com/teradata-labs/loom/pkg/types"
)

const (
	// DefaultBaseURL is the public Responses API (not ChatGPT's backend).
	DefaultBaseURL = "https://api.openai.com/v1"
	// DefaultTimeout bounds one request, stream included.
	DefaultTimeout = 10 * time.Minute
)

// Tokens hands out a valid access token (TokenSource does).
type Tokens interface {
	Token(ctx context.Context) (string, error)
	// Invalidate forces a refresh after the API refused the token.
	Invalidate()
}

// Config configures a Client.
type Config struct {
	// Model is a model slug from ListModels. Required.
	Model string
	// Tokens supplies the signed-in user's access token. Required.
	Tokens Tokens
	// UserID is the loom user the plan belongs to: requests whose context
	// carries another user ID (types.UserIDFromContext) are refused with
	// ErrOtherUser. "" accepts only requests without a user ID (a server
	// without authentication, used by one person).
	UserID string
	// ReasoningEffort is sent as reasoning.effort ("" leaves it out).
	ReasoningEffort string
	// BaseURL is the API base (default DefaultBaseURL).
	BaseURL string
	// HTTPClient sends requests (default: DefaultTimeout).
	HTTPClient *http.Client
}

// Client answers with a ChatGPT user's plan. It has no rate limiter: plan
// usage has no per-minute token limits to pace, and a request refused for
// the plan's usage limit (HTTP 429, ErrUsageLimit) must not be retried.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// NewClient returns a Client. It does not contact the API.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Model == "" {
		return nil, errors.New("chatgpt: a model is required (see ListModels)")
	}
	if cfg.Tokens == nil {
		return nil, errors.New("chatgpt: Config.Tokens is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	c := &Client{cfg: cfg, httpClient: cfg.HTTPClient}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	return c, nil
}

// Name returns "chatgpt".
func (c *Client) Name() string { return "chatgpt" }

// Model returns the model slug.
func (c *Client) Model() string { return c.cfg.Model }

// CostsNothing reports that calls have no per-call charge: they count
// against the user's plan usage limits, and Usage.CostUSD is 0.
func (c *Client) CostsNothing() bool { return true }

// Chat answers one conversation turn.
func (c *Client) Chat(ctx context.Context, messages []llmtypes.Message, tools []shuttle.Tool) (*llmtypes.LLMResponse, error) {
	return c.ChatStream(ctx, messages, tools, nil)
}

// ChatStream answers one conversation turn, calling tokenCallback with each
// piece of text as it arrives. Plan usage is always streamed.
func (c *Client) ChatStream(ctx context.Context, messages []llmtypes.Message, tools []shuttle.Tool, tokenCallback llmtypes.TokenCallback) (*llmtypes.LLMResponse, error) {
	if user := types.UserIDFromContext(ctx); user != c.cfg.UserID {
		return nil, ErrOtherUser
	}
	instructions, input := buildInput(messages)
	apiTools, names := buildTools(tools)
	req := responsesRequest{
		Model:        c.cfg.Model,
		Instructions: instructions,
		Input:        input,
		Tools:        apiTools,
		Stream:       true,
		Store:        false,
	}
	if c.cfg.ReasoningEffort != "" {
		req.Reasoning = &reasoningParam{Effort: c.cfg.ReasoningEffort}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("chatgpt: encode request: %w", err)
	}

	resp, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var onText func(string)
	if tokenCallback != nil {
		onText = func(s string) {
			llmtypes.NotifyStreamActivity(ctx)
			tokenCallback(s)
		}
	}
	out, err := readStream(resp.Body, names, onText)
	if err != nil {
		return nil, err
	}
	out.Usage.CostUSD = 0
	return out, nil
}

// post sends a request with the user's access token, refreshing it once if
// the API refuses it.
func (c *Client) post(ctx context.Context, body []byte) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		token, err := c.cfg.Tokens.Token(ctx)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("chatgpt: request: %w", err)
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			c.cfg.Tokens.Invalidate()
			continue
		}
		return nil, httpError(resp.StatusCode, data)
	}
}

// Model is one model the signed-in user's plan offers.
type Model struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Visibility  string `json:"visibility"`
}

// ListModels returns the models to offer the signed-in user: those the API
// lists with visibility "list". Pass a Model's Slug as Config.Model.
func ListModels(ctx context.Context, tokens Tokens, baseURL string, httpClient *http.Client) ([]Model, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	token, err := tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chatgpt: list models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("chatgpt: list models: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, httpError(resp.StatusCode, data)
	}
	var list struct {
		Models []Model `json:"models"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("chatgpt: list models: %w", err)
	}
	var out []Model
	for _, m := range list.Models {
		if m.Visibility == "list" && m.Slug != "" {
			out = append(out, m)
		}
	}
	return out, nil
}

var (
	_ llmtypes.LLMProvider          = (*Client)(nil)
	_ llmtypes.StreamingLLMProvider = (*Client)(nil)
	_ Tokens                        = (*TokenSource)(nil)
)
