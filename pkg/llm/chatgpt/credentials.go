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

package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Credentials are one user's Sign in with ChatGPT tokens on one host.
type Credentials struct {
	// ClientID is the client ID issued at the first sign-in.
	ClientID string `json:"client_id"`
	// HostID identifies this host to the sign-in (ext_agent_host_id).
	HostID string `json:"host_id"`
	// Subject and Email identify the ChatGPT user (from the ID token).
	Subject string `json:"subject"`
	Email   string `json:"email,omitempty"`
	// AccessToken is sent as the bearer token; it expires at ExpiresAt.
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	// RefreshToken gets new tokens; it is replaced on every refresh.
	RefreshToken string `json:"refresh_token"`
	// Scope is the granted scope, space-separated.
	Scope string `json:"scope"`
}

// apply takes a token response into c: new tokens, expiry, and the scope,
// which must include plan usage.
func (c *Credentials) apply(tok *tokenResponse, now time.Time) error {
	c.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		c.RefreshToken = tok.RefreshToken
	}
	expiresIn := tok.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	c.ExpiresAt = now.Add(time.Duration(expiresIn) * time.Second)
	if tok.Scope != "" {
		c.Scope = tok.Scope
	}
	if !c.HasPlanUsage() {
		return fmt.Errorf("%w: the sign-in did not allow using the ChatGPT plan (scope %q)", ErrSignInAgain, c.Scope)
	}
	return nil
}

// HasPlanUsage reports whether the grant allows plan usage.
func (c *Credentials) HasPlanUsage() bool {
	for _, s := range strings.Fields(c.Scope) {
		if s == PlanUsageScope {
			return true
		}
	}
	return false
}

// Store keeps credentials in a JSON file readable only by its owner,
// written atomically. It never logs or prints tokens.
type Store struct {
	Path string
}

// Load reads the saved credentials; ErrNotSignedIn when there are none.
func (s Store) Load() (*Credentials, error) {
	data, err := os.ReadFile(s.Path) // #nosec G304 -- path comes from server configuration
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotSignedIn
	}
	if err != nil {
		return nil, fmt.Errorf("chatgpt: read credentials: %w", err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("chatgpt: read credentials %s: %w", s.Path, err)
	}
	if c.RefreshToken == "" && c.AccessToken == "" {
		return nil, ErrNotSignedIn
	}
	return &c, nil
}

// Save writes c atomically with mode 0600 (its directory 0700).
func (s Store) Save(c *Credentials) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("chatgpt: save credentials: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".chatgpt-*.json")
	if err != nil {
		return fmt.Errorf("chatgpt: save credentials: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chatgpt: save credentials: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chatgpt: save credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("chatgpt: save credentials: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.Path); err != nil {
		return fmt.Errorf("chatgpt: save credentials: %w", err)
	}
	return nil
}

// Delete removes the saved credentials (no error when there are none).
func (s Store) Delete() error {
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("chatgpt: delete credentials: %w", err)
	}
	return nil
}

// refreshMargin is how long before expiry an access token is refreshed.
const refreshMargin = 2 * time.Minute

// TokenSource hands out a valid access token from a Store, refreshing it
// when it is about to expire. Refreshes are serialized and each rotated
// refresh token is saved before it is used, as a reused refresh token
// revokes the grant. Safe for concurrent use within one process; two
// processes must not share one credentials file.
type TokenSource struct {
	Store Store
	Auth  *Auth
	// Now is the clock (default time.Now).
	Now func() time.Time

	mu    sync.Mutex
	creds *Credentials
}

func (t *TokenSource) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Credentials returns the current credentials, loading them when needed.
func (t *TokenSource) Credentials() (*Credentials, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.loadLocked(); err != nil {
		return nil, err
	}
	c := *t.creds
	return &c, nil
}

func (t *TokenSource) loadLocked() error {
	if t.creds != nil {
		return nil
	}
	c, err := t.Store.Load()
	if err != nil {
		return err
	}
	t.creds = c
	return nil
}

// Token returns a valid access token.
func (t *TokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.loadLocked(); err != nil {
		return "", err
	}
	if t.creds.AccessToken != "" && t.now().Add(refreshMargin).Before(t.creds.ExpiresAt) {
		return t.creds.AccessToken, nil
	}
	return t.refreshLocked(ctx)
}

// Invalidate forces the next Token call to refresh, after the API refused
// the current access token.
func (t *TokenSource) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.creds != nil {
		t.creds.ExpiresAt = time.Time{}
	}
}

func (t *TokenSource) refreshLocked(ctx context.Context) (string, error) {
	if t.Auth == nil {
		return "", errors.New("chatgpt: TokenSource.Auth is required to refresh")
	}
	next, err := t.Auth.Refresh(ctx, t.creds)
	if err != nil {
		return "", err
	}
	if err := t.Store.Save(next); err != nil {
		// The old refresh token is spent: keep the new one in memory so
		// this process keeps working, and report the save failure.
		t.creds = next
		return "", err
	}
	t.creds = next
	return next.AccessToken, nil
}
