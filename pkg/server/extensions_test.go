// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"go.uber.org/zap/zaptest"
)

// upstreamRecorder is a service next to loom that records what reaches it.
type upstreamRecorder struct {
	mu      sync.Mutex
	path    string
	headers http.Header
	body    string
	calls   int
}

func (u *upstreamRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.path, u.headers, u.body = r.URL.RequestURI(), r.Header.Clone(), string(body)
	u.calls++
	u.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// extensionFixture is a gateway with device pairing required and one
// extension route, and a device paired as "sam".
func extensionFixture(t *testing.T, allowDevices bool) (*HTTPServer, http.Handler, *upstreamRecorder, string) {
	t.Helper()
	rec := &upstreamRecorder{}
	upstream := httptest.NewServer(rec)
	t.Cleanup(upstream.Close)

	ds := newTestDeviceServer(t, true)
	created, err := ds.CreatePairingCode(context.Background(), &loomv1.CreatePairingCodeRequest{UserId: "sam"})
	require.NoError(t, err)
	redeemed, err := ds.RedeemPairingCode(context.Background(), &loomv1.RedeemPairingCodeRequest{Code: created.GetCode(), DeviceName: "Phone"})
	require.NoError(t, err)

	h := NewHTTPServer(nil, "127.0.0.1:0", "127.0.0.1:0", zaptest.NewLogger(t))
	h.SetStreamInterceptors(ds.StreamServerInterceptor(), UserIDStreamInterceptor(UserIDConfig{}))
	route, err := ParseExtensionRoute("household", "/v1/household/", upstream.URL, "s3cret", allowDevices)
	require.NoError(t, err)
	require.NoError(t, h.AddExtensionRoute(route))
	if allowDevices {
		ds.AllowMethod(ExtensionMethod("household"))
	}
	return h, h.extensionHandler(route), rec, redeemed.GetDeviceToken()
}

func TestExtensionRouteForwardsWhoIsCalling(t *testing.T) {
	_, handler, rec, token := extensionFixture(t, true)

	req := httptest.NewRequest(http.MethodPost, "/v1/household/notes?limit=5", strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(ExtensionUserIDHeader, "alex")    // a client cannot claim to be someone else
	req.Header.Set(ExtensionSecretHeader, "guessed") // nor supply the secret
	req.Header.Set("X-User-Id", "alex")
	req.Header.Set("Cookie", "session=x")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"ok":true}`, w.Body.String())
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Equal(t, "/v1/household/notes?limit=5", rec.path, "the path and query are forwarded unchanged")
	assert.Equal(t, `{"text":"hi"}`, rec.body)
	assert.Equal(t, "sam", rec.headers.Get(ExtensionUserIDHeader), "the device's user, not the header the client sent")
	assert.Equal(t, "s3cret", rec.headers.Get(ExtensionSecretHeader))
	assert.Empty(t, rec.headers.Get("Authorization"), "the device token stays with loom")
	assert.Empty(t, rec.headers.Get("Cookie"))
	assert.Empty(t, rec.headers.Get("X-User-Id"))
}

func TestExtensionRouteRejectsBeforeForwarding(t *testing.T) {
	_, handler, rec, token := extensionFixture(t, true)

	for name, auth := range map[string]string{
		"no credential":   "",
		"unknown device":  "Bearer loomd_not-a-real-token",
		"unknown bearer":  "Bearer something-else",
		"malformed token": "Basic Zm9vOmJhcg==",
	} {
		req := httptest.NewRequest(http.MethodGet, "/v1/household/notes", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code, name)
	}
	rec.mu.Lock()
	assert.Zero(t, rec.calls, "nothing reached the service")
	rec.mu.Unlock()

	// The paired device still works.
	req := httptest.NewRequest(http.MethodGet, "/v1/household/notes", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestExtensionRouteClosedToDevicesUnlessAllowed(t *testing.T) {
	_, handler, rec, token := extensionFixture(t, false)
	req := httptest.NewRequest(http.MethodGet, "/v1/household/notes", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	rec.mu.Lock()
	assert.Zero(t, rec.calls)
	rec.mu.Unlock()
}

func TestExtensionRouteUpstreamDown(t *testing.T) {
	ds := newTestDeviceServer(t, false)
	h := NewHTTPServer(nil, "127.0.0.1:0", "127.0.0.1:0", zaptest.NewLogger(t))
	h.SetStreamInterceptors(ds.StreamServerInterceptor())
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	route, err := ParseExtensionRoute("household", "/v1/household/", url, "", false)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h.extensionHandler(route).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/household/notes", nil))
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.NotContains(t, w.Body.String(), "127.0.0.1", "the service's address is not shown to clients")
}

func TestParseExtensionRoute(t *testing.T) {
	_, err := ParseExtensionRoute("household", "/v1/household/", "http://127.0.0.1:5017", "", true)
	assert.NoError(t, err)
	_, err = ParseExtensionRoute("household", "/v1/household/", "http://localhost:5017/base", "", true)
	assert.NoError(t, err)
	_, err = ParseExtensionRoute("household", "/v1/household/", "http://[::1]:5017", "", true)
	assert.NoError(t, err)

	bad := map[string][3]string{
		"remote upstream":   {"household", "/v1/household/", "http://192.168.1.20:5017"},
		"public upstream":   {"household", "/v1/household/", "https://example.com"},
		"not a URL":         {"household", "/v1/household/", "127.0.0.1:5017"},
		"one segment":       {"household", "/household/", "http://127.0.0.1:5017"},
		"no trailing slash": {"household", "/v1/household", "http://127.0.0.1:5017"},
		"root":              {"household", "/", "http://127.0.0.1:5017"},
		"loom's apps":       {"household", "/apps/x/", "http://127.0.0.1:5017"},
		"loom's weave":      {"household", "/v1/weave/", "http://127.0.0.1:5017"},
		"traversal":         {"household", "/v1/../x/", "http://127.0.0.1:5017"},
		"custom verb":       {"household", "/v1/x:y/", "http://127.0.0.1:5017"},
		"bad name":          {"House Hold", "/v1/household/", "http://127.0.0.1:5017"},
	}
	for name, c := range bad {
		_, err := ParseExtensionRoute(c[0], c[1], c[2], "", false)
		assert.Error(t, err, name)
	}
}

func TestAddExtensionRouteRejectsOverlap(t *testing.T) {
	h := NewHTTPServer(nil, "127.0.0.1:0", "127.0.0.1:0", zaptest.NewLogger(t))
	a, err := ParseExtensionRoute("household", "/v1/household/", "http://127.0.0.1:5017", "", false)
	require.NoError(t, err)
	b, err := ParseExtensionRoute("notes", "/v1/household/notes/", "http://127.0.0.1:5018", "", false)
	require.NoError(t, err)
	require.NoError(t, h.AddExtensionRoute(a))
	assert.Error(t, h.AddExtensionRoute(b))
}
