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
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Sign in with ChatGPT, open-source track: OAuth 2.0 authorization code
// with PKCE (S256), a loopback redirect, no client secret. The first sign-in
// registers the app with client_id=dynamic_agent_client; the callback
// returns the issued client ID, which every later request uses.
const (
	// DefaultIssuer is OpenAI's OpenID Connect issuer.
	DefaultIssuer = "https://auth.openai.com"
	// DefaultResource is the audience plan-usage tokens are issued for.
	DefaultResource = "https://api.openai.com/v1"
	// RegistrationClientID is the first-sign-in entry point; it is never
	// saved or used for token exchange.
	RegistrationClientID = "dynamic_agent_client"
	// PlanUsageScope is the scope a grant needs for plan usage.
	PlanUsageScope = "chatgpt.tokens.use.direct"
	// CallbackPath is the loopback redirect's path.
	CallbackPath = "/auth/callback"
)

// Scopes are the scopes requested: identity, a refresh token, and plan
// usage through the Responses API.
var Scopes = []string{"openid", "profile", "email", "offline_access", "resource.invoke", PlanUsageScope}

// Auth signs a user in with ChatGPT and refreshes their tokens.
type Auth struct {
	// Issuer is the OpenID Connect issuer (default DefaultIssuer).
	Issuer string
	// Resource is the token audience (default DefaultResource).
	Resource string
	// HTTPClient sends the requests (default: 30 s timeout).
	HTTPClient *http.Client
	// Now is the clock tokens' expiry is computed from (default time.Now).
	Now func() time.Time

	mu        sync.Mutex
	discovery *discovery
	keys      map[string]*rsa.PublicKey
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

func (a *Auth) issuer() string {
	if a.Issuer != "" {
		return strings.TrimSuffix(a.Issuer, "/")
	}
	return DefaultIssuer
}

func (a *Auth) resource() string {
	if a.Resource != "" {
		return a.Resource
	}
	return DefaultResource
}

func (a *Auth) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Auth) client() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// discover reads the issuer's OpenID configuration once.
func (a *Auth) discover(ctx context.Context) (*discovery, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.discovery != nil {
		return a.discovery, nil
	}
	var d discovery
	if err := a.getJSON(ctx, a.issuer()+"/.well-known/openid-configuration", &d); err != nil {
		return nil, fmt.Errorf("chatgpt: read OpenID configuration: %w", err)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, errors.New("chatgpt: OpenID configuration lacks authorization, token or JWKS endpoint")
	}
	if strings.TrimSuffix(d.Issuer, "/") != a.issuer() {
		return nil, fmt.Errorf("chatgpt: OpenID configuration issuer %q, want %q", d.Issuer, a.issuer())
	}
	a.discovery = &d
	return &d, nil
}

func (a *Auth) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// SignInOptions configure one sign-in.
type SignInOptions struct {
	// AppName is shown to the user on the first sign-in (agent_name_hint).
	AppName string
	// Previous is the credentials of an earlier sign-in on this host, if
	// any: its client ID and host ID are reused, so the app is not
	// registered again.
	Previous *Credentials
	// OpenBrowser opens the sign-in page in the system browser (not an
	// embedded web view). Required.
	OpenBrowser func(signInURL string) error
	// Port is the loopback port for the redirect; 0 picks a free one.
	Port int
}

// SignIn runs one sign-in: it listens on 127.0.0.1, opens the sign-in page,
// waits for the redirect (or ctx), exchanges the code, checks the ID token,
// and returns the credentials. The grant must include plan usage
// (PlanUsageScope), or ErrSignInAgain is returned.
func (a *Auth) SignIn(ctx context.Context, opts SignInOptions) (*Credentials, error) {
	if opts.OpenBrowser == nil {
		return nil, errors.New("chatgpt: SignInOptions.OpenBrowser is required")
	}
	d, err := a.discover(ctx)
	if err != nil {
		return nil, err
	}
	hostID := ""
	clientID := RegistrationClientID
	if opts.Previous != nil {
		hostID, clientID = opts.Previous.HostID, opts.Previous.ClientID
	}
	if hostID == "" {
		if hostID, err = newHostID(); err != nil {
			return nil, err
		}
	}
	if clientID == "" {
		clientID = RegistrationClientID
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", opts.Port))
	if err != nil {
		return nil, fmt.Errorf("chatgpt: listen for the sign-in redirect: %w", err)
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", ln.Addr().(*net.TCPAddr).Port, CallbackPath)

	state, err := randomString(32)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	nonce, err := randomString(32)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	verifier, err := randomString(64)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))

	q := url.Values{
		"client_id":             {clientID},
		"ext_agent_host_id":     {hostID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"scope":                 {strings.Join(Scopes, " ")},
		"resource":              {a.resource()},
	}
	if clientID == RegistrationClientID && opts.AppName != "" {
		q.Set("agent_name_hint", opts.AppName)
	}
	if opts.Previous != nil && opts.Previous.Email != "" {
		q.Set("login_hint", opts.Previous.Email)
	}
	authURL := d.AuthorizationEndpoint + "?" + q.Encode()

	type result struct {
		code, clientID string
		err            error
	}
	results := make(chan result, 1)
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != CallbackPath {
				http.NotFound(w, r)
				return
			}
			p := r.URL.Query()
			var res result
			switch {
			case p.Get("state") != state:
				res.err = errors.New("chatgpt: the sign-in redirect's state does not match")
			case p.Get("error") != "":
				res.err = fmt.Errorf("chatgpt: sign-in refused: %s %s", p.Get("error"), p.Get("error_description"))
			case p.Get("code") == "":
				res.err = errors.New("chatgpt: the sign-in redirect has no code")
			default:
				res.code, res.clientID = p.Get("code"), p.Get("client_id")
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if res.err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "<!doctype html><title>Sign-in failed</title><p>Sign-in didn't finish. You can close this window and try again.</p>")
			} else {
				_, _ = io.WriteString(w, "<!doctype html><title>Signed in</title><p>You're signed in. You can close this window.</p>")
			}
			select {
			case results <- res:
			default:
			}
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := opts.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("chatgpt: open the sign-in page: %w", err)
	}
	var res result
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res = <-results:
	}
	if res.err != nil {
		return nil, res.err
	}
	issued := res.clientID
	if issued == "" {
		issued = clientID
	}
	if issued == RegistrationClientID {
		return nil, errors.New("chatgpt: the sign-in redirect did not return the app's client ID")
	}

	tok, err := a.token(ctx, d, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {issued},
		"code":          {res.code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"resource":      {a.resource()},
	})
	if err != nil {
		return nil, err
	}
	claims, err := a.verifyIDToken(ctx, d, tok.IDToken, issued, nonce)
	if err != nil {
		return nil, err
	}
	creds := &Credentials{ClientID: issued, HostID: hostID, Subject: claims.Subject, Email: claims.Email}
	if err := creds.apply(tok, a.now()); err != nil {
		return nil, err
	}
	return creds, nil
}

// tokenResponse is the token endpoint's answer.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

// tokenError is an OAuth error response.
type tokenError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// signInAgainCodes are token-endpoint errors that mean the saved grant is
// gone and the user has to sign in again.
var signInAgainCodes = map[string]bool{
	"invalid_grant":         true,
	"refresh_token_reused":  true,
	"refresh_token_expired": true,
	"refresh_token_invalid": true,
	"access_denied":         true,
}

func (a *Auth) token(ctx context.Context, d *discovery, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("chatgpt: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("chatgpt: token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var te tokenError
		_ = json.Unmarshal(body, &te)
		if signInAgainCodes[te.Error] {
			return nil, fmt.Errorf("%w: %s %s", ErrSignInAgain, te.Error, te.ErrorDescription)
		}
		if te.Error != "" {
			return nil, fmt.Errorf("chatgpt: token request refused: %s %s (HTTP %d)", te.Error, te.ErrorDescription, resp.StatusCode)
		}
		return nil, fmt.Errorf("chatgpt: token request: HTTP %d", resp.StatusCode)
	}
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("chatgpt: token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("chatgpt: token response has no access token")
	}
	return &tok, nil
}

// Refresh exchanges creds' refresh token for new tokens and returns the
// updated credentials (the refresh token is replaced on every refresh, so
// the result must be saved before the old one is used again). scope is left
// out, which keeps the grant. A grant that is gone returns ErrSignInAgain.
func (a *Auth) Refresh(ctx context.Context, creds *Credentials) (*Credentials, error) {
	if creds == nil || creds.RefreshToken == "" || creds.ClientID == "" {
		return nil, ErrSignInAgain
	}
	d, err := a.discover(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := a.token(ctx, d, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {creds.ClientID},
		"refresh_token": {creds.RefreshToken},
		"resource":      {a.resource()},
	})
	if err != nil {
		return nil, err
	}
	next := *creds
	if err := next.apply(tok, a.now()); err != nil {
		return nil, err
	}
	return &next, nil
}

// Revoke revokes creds' refresh token (signing out). An issuer without a
// revocation endpoint is an error; the caller should delete the saved
// credentials either way.
func (a *Auth) Revoke(ctx context.Context, creds *Credentials) error {
	if creds == nil || creds.RefreshToken == "" {
		return nil
	}
	d, err := a.discover(ctx)
	if err != nil {
		return err
	}
	if d.RevocationEndpoint == "" {
		return errors.New("chatgpt: the issuer has no revocation endpoint")
	}
	form := url.Values{"token": {creds.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {creds.ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.RevocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client().Do(req)
	if err != nil {
		return fmt.Errorf("chatgpt: revoke: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chatgpt: revoke: HTTP %d", resp.StatusCode)
	}
	return nil
}

// idClaims are the ID token claims the sign-in checks and keeps.
type idClaims struct {
	Issuer   string          `json:"iss"`
	Subject  string          `json:"sub"`
	Audience json.RawMessage `json:"aud"`
	Expiry   int64           `json:"exp"`
	Nonce    string          `json:"nonce"`
	Email    string          `json:"email"`
}

func (c idClaims) hasAudience(aud string) bool {
	var one string
	if json.Unmarshal(c.Audience, &one) == nil {
		return one == aud
	}
	var many []string
	if json.Unmarshal(c.Audience, &many) == nil {
		for _, a := range many {
			if a == aud {
				return true
			}
		}
	}
	return false
}

// verifyIDToken checks the ID token's RS256 signature against the issuer's
// JWKS, its issuer, audience (the issued client ID), expiry and nonce.
func (a *Auth) verifyIDToken(ctx context.Context, d *discovery, token, clientID, nonce string) (*idClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("chatgpt: the sign-in returned no valid ID token")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("chatgpt: ID token header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("chatgpt: ID token header: %w", err)
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("chatgpt: ID token algorithm %q, want RS256", header.Alg)
	}
	key, err := a.key(ctx, d, header.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("chatgpt: ID token signature: %w", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return nil, errors.New("chatgpt: the ID token's signature does not verify")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("chatgpt: ID token claims: %w", err)
	}
	var claims idClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("chatgpt: ID token claims: %w", err)
	}
	switch {
	case strings.TrimSuffix(claims.Issuer, "/") != a.issuer():
		return nil, fmt.Errorf("chatgpt: ID token issuer %q", claims.Issuer)
	case !claims.hasAudience(clientID):
		return nil, errors.New("chatgpt: the ID token is not for this app")
	case claims.Expiry != 0 && a.now().After(time.Unix(claims.Expiry, 0).Add(time.Minute)):
		return nil, errors.New("chatgpt: the ID token has expired")
	case claims.Nonce != nonce:
		return nil, errors.New("chatgpt: the ID token's nonce does not match")
	case claims.Subject == "":
		return nil, errors.New("chatgpt: the ID token names no user")
	}
	return &claims, nil
}

// key returns the issuer's RSA key kid, fetching the JWKS when it is not
// known yet (keys rotate).
func (a *Auth) key(ctx context.Context, d *discovery, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	k := a.keys[kid]
	a.mu.Unlock()
	if k != nil {
		return k, nil
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := a.getJSON(ctx, d.JWKSURI, &set); err != nil {
		return nil, fmt.Errorf("chatgpt: read signing keys: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, jk := range set.Keys {
		if jk.Kty != "RSA" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(jk.N)
		if err != nil {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(jk.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		keys[jk.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	a.mu.Lock()
	a.keys = keys
	a.mu.Unlock()
	if k := keys[kid]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("chatgpt: no signing key %q", kid)
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("chatgpt: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newHostID returns a stable-per-host identifier in a form the sign-in
// accepts (urn:uuid, a random version 4 UUID). It is not derived from the
// user.
func newHostID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("chatgpt: random: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
