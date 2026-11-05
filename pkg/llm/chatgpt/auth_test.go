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
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIssuer is an OpenID provider that signs users in the way Sign in
// with ChatGPT does: dynamic registration on the first sign-in, PKCE,
// rotating refresh tokens.
type fakeIssuer struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	scope  string
	mu     sync.Mutex
	codes  map[string]authRequest
	tokens map[string]bool // live refresh tokens
	seq    int
	// lastAuthorize is the last authorize request's query.
	lastAuthorize url.Values
	revoked       []string
}

type authRequest struct {
	clientID, challenge, redirect, nonce string
}

const issuedClientID = "oaiapp_test123"

func newFakeIssuer(t *testing.T) *fakeIssuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeIssuer{t: t, key: key, scope: strings.Join(Scopes, " "), codes: map[string]authRequest{}, tokens: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.srv.URL,
			"authorization_endpoint": f.srv.URL + "/api/accounts/authorize",
			"token_endpoint":         f.srv.URL + "/api/accounts/oauth/token",
			"revocation_endpoint":    f.srv.URL + "/oauth/revoke",
			"jwks_uri":               f.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		e := big.NewInt(int64(key.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	})
	mux.HandleFunc("/api/accounts/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		f.lastAuthorize = q
		f.seq++
		code := "code-" + string(rune('a'+f.seq))
		f.codes[code] = authRequest{clientID: issuedClientID, challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), nonce: q.Get("nonce")}
		f.mu.Unlock()
		back, _ := url.Parse(q.Get("redirect_uri"))
		bq := back.Query()
		bq.Set("code", code)
		bq.Set("state", q.Get("state"))
		if q.Get("client_id") == RegistrationClientID {
			bq.Set("client_id", issuedClientID)
		}
		back.RawQuery = bq.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	})
	mux.HandleFunc("/api/accounts/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		f.mu.Lock()
		defer f.mu.Unlock()
		fail := func(code string) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
		}
		if r.Form.Get("resource") != DefaultResource {
			fail("invalid_target")
			return
		}
		if r.Form.Get("client_id") != issuedClientID {
			fail("invalid_client")
			return
		}
		var nonce string
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			ar, ok := f.codes[r.Form.Get("code")]
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || ar.redirect != r.Form.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != ar.challenge {
				fail("invalid_grant")
				return
			}
			delete(f.codes, r.Form.Get("code"))
			nonce = ar.nonce
		case "refresh_token":
			if r.Form.Get("scope") != "" {
				fail("invalid_scope")
				return
			}
			rt := r.Form.Get("refresh_token")
			if !f.tokens[rt] {
				fail("refresh_token_reused")
				return
			}
			delete(f.tokens, rt)
		default:
			fail("unsupported_grant_type")
			return
		}
		f.seq++
		refresh := "refresh-" + string(rune('a'+f.seq))
		f.tokens[refresh] = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-" + string(rune('a'+f.seq)),
			"refresh_token": refresh,
			"id_token":      f.idToken(nonce),
			"expires_in":    3600,
			"scope":         f.scope,
			"token_type":    "Bearer",
		})
	})
	mux.HandleFunc("/oauth/revoke", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		f.mu.Lock()
		defer f.mu.Unlock()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		delete(f.tokens, r.Form.Get("token"))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) idToken(nonce string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"k1","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iss": f.srv.URL, "sub": "user-1", "aud": issuedClientID, "email": "sam@example.com",
		"exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce,
	})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(header + "." + payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	require.NoError(f.t, err)
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// browser follows the sign-in page's redirect back to the loopback
// listener, as the user's browser would.
func browser(t *testing.T) func(string) error {
	return func(u string) error {
		resp, err := http.Get(u) // #nosec G107 -- test server URL
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	}
}

func TestSignInRegistersThenReuses(t *testing.T) {
	f := newFakeIssuer(t)
	a := &Auth{Issuer: f.srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creds, err := a.SignIn(ctx, SignInOptions{AppName: "Weaver", OpenBrowser: browser(t)})
	require.NoError(t, err)
	assert.Equal(t, issuedClientID, creds.ClientID, "the issued client ID is kept, not dynamic_agent_client")
	assert.Equal(t, "user-1", creds.Subject)
	assert.Equal(t, "sam@example.com", creds.Email)
	assert.True(t, creds.HasPlanUsage())
	assert.True(t, strings.HasPrefix(creds.HostID, "urn:uuid:"))

	q := f.lastAuthorize
	assert.Equal(t, RegistrationClientID, q.Get("client_id"))
	assert.Equal(t, "Weaver", q.Get("agent_name_hint"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, DefaultResource, q.Get("resource"))
	assert.Equal(t, strings.Join(Scopes, " "), q.Get("scope"))
	assert.True(t, strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:"), "loopback address, not localhost")
	assert.True(t, strings.HasSuffix(q.Get("redirect_uri"), CallbackPath))

	again, err := a.SignIn(ctx, SignInOptions{AppName: "Weaver", Previous: creds, OpenBrowser: browser(t)})
	require.NoError(t, err)
	q = f.lastAuthorize
	assert.Equal(t, issuedClientID, q.Get("client_id"), "a returning sign-in uses the issued client ID")
	assert.Empty(t, q.Get("agent_name_hint"))
	assert.Equal(t, creds.HostID, q.Get("ext_agent_host_id"), "the same host ID")
	assert.Equal(t, creds.HostID, again.HostID)
}

func TestSignInWithoutPlanUsage(t *testing.T) {
	f := newFakeIssuer(t)
	f.scope = "openid profile email offline_access"
	a := &Auth{Issuer: f.srv.URL}
	_, err := a.SignIn(context.Background(), SignInOptions{OpenBrowser: browser(t)})
	assert.ErrorIs(t, err, ErrSignInAgain)
}

func TestSignInRejectsBadIDToken(t *testing.T) {
	f := newFakeIssuer(t)
	a := &Auth{Issuer: f.srv.URL}
	ctx := context.Background()
	d, err := a.discover(ctx)
	require.NoError(t, err)

	_, err = a.verifyIDToken(ctx, d, f.idToken("n"), issuedClientID, "n")
	require.NoError(t, err, "the issuer's own token verifies")
	_, err = a.verifyIDToken(ctx, d, f.idToken("n"), issuedClientID, "other-nonce")
	assert.ErrorContains(t, err, "nonce")
	_, err = a.verifyIDToken(ctx, d, f.idToken("n"), "someone-else", "n")
	assert.ErrorContains(t, err, "not for this app")

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	a.keys = map[string]*rsa.PublicKey{"k1": &other.PublicKey}
	_, err = a.verifyIDToken(ctx, d, f.idToken("n"), issuedClientID, "n")
	assert.ErrorContains(t, err, "signature", "a token not signed by the issuer's key")
}

func TestTokenSourceRefreshRotatesAndSaves(t *testing.T) {
	f := newFakeIssuer(t)
	a := &Auth{Issuer: f.srv.URL}
	creds, err := a.SignIn(context.Background(), SignInOptions{OpenBrowser: browser(t)})
	require.NoError(t, err)
	store := Store{Path: filepath.Join(t.TempDir(), "chatgpt.json")}
	require.NoError(t, store.Save(creds))
	info, err := os.Stat(store.Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "owner-only")

	var clock sync.Mutex
	now := time.Now()
	nowFn := func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	a.Now = nowFn
	ts := &TokenSource{Store: store, Auth: a, Now: nowFn}
	tok, err := ts.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, creds.AccessToken, tok, "a fresh token is used as is")

	clock.Lock()
	now = creds.ExpiresAt.Add(-time.Minute) // inside the refresh margin
	clock.Unlock()
	var wg sync.WaitGroup
	got := make([]string, 8)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], _ = ts.Token(context.Background())
		}(i)
	}
	wg.Wait()
	for _, g := range got {
		assert.Equal(t, got[0], g, "concurrent callers share one refresh")
	}
	assert.NotEqual(t, creds.AccessToken, got[0])

	saved, err := store.Load()
	require.NoError(t, err)
	assert.NotEqual(t, creds.RefreshToken, saved.RefreshToken, "the rotated refresh token is saved")

	// The old refresh token is spent: using it again means signing in again.
	_, err = a.Refresh(context.Background(), creds)
	assert.ErrorIs(t, err, ErrSignInAgain)

	require.NoError(t, a.Revoke(context.Background(), saved))
	assert.Equal(t, []string{saved.RefreshToken}, f.revoked)
	require.NoError(t, store.Delete())
	_, err = store.Load()
	assert.ErrorIs(t, err, ErrNotSignedIn)
}
