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

package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/teradata-labs/loom/internal/uiutil"
	"github.com/teradata-labs/loom/pkg/agent"
	loomconfig "github.com/teradata-labs/loom/pkg/config"
	"github.com/teradata-labs/loom/pkg/llm/chatgpt"
)

// chatgptCredentialsPath is llm.chatgpt_credentials_file, or
// $LOOM_DATA_DIR/chatgpt.json.
func chatgptCredentialsPath(cfg LLMConfig) string {
	if cfg.ChatGPTCredentialsFile != "" {
		return cfg.ChatGPTCredentialsFile
	}
	return filepath.Join(loomconfig.GetLoomDataDir(), "chatgpt.json")
}

// chatgptSources holds one TokenSource per credentials file, so every
// provider in this process shares its refreshes: a refresh token is
// replaced on every refresh, and two refreshes with the same one revoke
// the grant.
var chatgptSources = struct {
	sync.Mutex
	m map[string]*chatgpt.TokenSource
}{m: map[string]*chatgpt.TokenSource{}}

func chatgptTokenSource(cfg LLMConfig) *chatgpt.TokenSource {
	path := chatgptCredentialsPath(cfg)
	chatgptSources.Lock()
	defer chatgptSources.Unlock()
	ts, ok := chatgptSources.m[path]
	if !ok {
		ts = &chatgpt.TokenSource{Store: chatgpt.Store{Path: path}, Auth: &chatgpt.Auth{}}
		chatgptSources.m[path] = ts
	}
	return ts
}

// newChatGPTProvider builds the chatgpt provider for model (default
// llm.chatgpt_model) with the signed-in user's credentials, bound to
// llm.chatgpt_user_id.
func newChatGPTProvider(cfg LLMConfig, model string) (agent.LLMProvider, error) {
	if model == "" {
		model = cfg.ChatGPTModel
	}
	ts := chatgptTokenSource(cfg)
	if _, err := ts.Credentials(); err != nil {
		if errors.Is(err, chatgpt.ErrNotSignedIn) {
			return nil, fmt.Errorf("chatgpt: not signed in (run 'looms chatgpt login')")
		}
		return nil, err
	}
	return chatgpt.NewClient(chatgpt.Config{
		Model:           model,
		Tokens:          ts,
		UserID:          cfg.ChatGPTUserID,
		ReasoningEffort: cfg.ChatGPTReasoningEffort,
	})
}

var (
	chatgptLoginPort      int
	chatgptLoginNoBrowser bool
	chatgptLoginAppName   string
)

var chatgptCmd = &cobra.Command{
	Use:   "chatgpt",
	Short: "Sign in with ChatGPT to answer with a ChatGPT plan",
	Long: `Sign in with ChatGPT so the "chatgpt" provider answers with your ChatGPT
plan's usage instead of an API key.

A plan answers only the person who signed in. Set llm.chatgpt_user_id to
that person's loom user ID on a server with authentication; requests for
anyone else are refused.

Examples:
  looms chatgpt login     # sign in (opens the browser)
  looms chatgpt status    # who is signed in
  looms chatgpt models    # models the plan offers
  looms chatgpt logout    # sign out and delete the saved credentials`,
}

var chatgptLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in with ChatGPT (opens the browser)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
		defer cancel()
		store := chatgpt.Store{Path: chatgptCredentialsPath(config.LLM)}
		previous, err := store.Load()
		if err != nil && !errors.Is(err, chatgpt.ErrNotSignedIn) {
			return err
		}
		open := func(u string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "Sign in with ChatGPT:\n  %s\n", u)
			if chatgptLoginNoBrowser {
				return nil
			}
			if err := uiutil.OpenURL(u); err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "Open the address above in a browser on this computer.")
			}
			return nil
		}
		auth := &chatgpt.Auth{}
		creds, err := auth.SignIn(ctx, chatgpt.SignInOptions{
			AppName:     chatgptLoginAppName,
			Previous:    previous,
			OpenBrowser: open,
			Port:        chatgptLoginPort,
		})
		if err != nil {
			return err
		}
		if err := store.Save(creds); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Signed in as %s. Credentials saved to %s.\n", nameOf(creds), store.Path)
		return nil
	},
}

var chatgptLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Sign out of ChatGPT and delete the saved credentials",
	RunE: func(cmd *cobra.Command, _ []string) error {
		store := chatgpt.Store{Path: chatgptCredentialsPath(config.LLM)}
		creds, err := store.Load()
		if errors.Is(err, chatgpt.ErrNotSignedIn) {
			fmt.Fprintln(cmd.OutOrStdout(), "Not signed in.")
			return nil
		}
		if err != nil {
			return err
		}
		revokeErr := (&chatgpt.Auth{}).Revoke(cmd.Context(), creds)
		if err := store.Delete(); err != nil {
			return err
		}
		if revokeErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Credentials deleted, but signing out at OpenAI failed: %v\n", revokeErr)
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Signed out.")
		return nil
	},
}

var chatgptStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show who is signed in with ChatGPT",
	RunE: func(cmd *cobra.Command, _ []string) error {
		creds, err := chatgpt.Store{Path: chatgptCredentialsPath(config.LLM)}.Load()
		if errors.Is(err, chatgpt.ErrNotSignedIn) {
			fmt.Fprintln(cmd.OutOrStdout(), "Not signed in (run 'looms chatgpt login').")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Signed in as %s\nPlan usage allowed: %v\nAccess token expires: %s\n",
			nameOf(creds), creds.HasPlanUsage(), creds.ExpiresAt.Local().Format(time.RFC1123))
		return nil
	},
}

var chatgptModelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List the models the signed-in ChatGPT plan offers",
	RunE: func(cmd *cobra.Command, _ []string) error {
		models, err := chatgpt.ListModels(cmd.Context(), chatgptTokenSource(config.LLM), "", nil)
		if err != nil {
			return err
		}
		for _, m := range models {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", m.Slug, m.DisplayName)
		}
		return nil
	},
}

func nameOf(c *chatgpt.Credentials) string {
	if c.Email != "" {
		return c.Email
	}
	return c.Subject
}

func init() {
	rootCmd.AddCommand(chatgptCmd)
	chatgptCmd.AddCommand(chatgptLoginCmd, chatgptLogoutCmd, chatgptStatusCmd, chatgptModelsCmd)
	chatgptLoginCmd.Flags().IntVar(&chatgptLoginPort, "port", 0, "loopback port for the sign-in redirect (default: any free port)")
	chatgptLoginCmd.Flags().BoolVar(&chatgptLoginNoBrowser, "no-browser", false, "print the sign-in address instead of opening the browser")
	chatgptLoginCmd.Flags().StringVar(&chatgptLoginAppName, "app-name", "Loom", "app name shown on the first sign-in")
}
