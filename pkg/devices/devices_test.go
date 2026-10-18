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

package devices

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newManager(t *testing.T) (*Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "devices.json")
	m, err := Open(path)
	require.NoError(t, err)
	return m, path
}

func TestRedeemIssuesWorkingToken(t *testing.T) {
	m, _ := newManager(t)
	code, expires, err := m.CreateCode("default-user", 10*time.Minute)
	require.NoError(t, err)
	assert.Len(t, code, 32)
	assert.True(t, expires.After(time.Now()))

	d, token, err := m.Redeem(strings.ToLower(code), "  Josh's iPhone ", "ios")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(token, TokenPrefix))
	assert.Equal(t, "Josh's iPhone", d.Name)
	assert.Equal(t, "default-user", d.UserID)
	assert.NotContains(t, d.TokenHash, token)

	got, ok := m.Authenticate(token)
	require.True(t, ok)
	assert.Equal(t, d.ID, got.ID)
}

func TestCodeWorksOnce(t *testing.T) {
	m, _ := newManager(t)
	code, _, err := m.CreateCode("u", time.Minute)
	require.NoError(t, err)
	_, _, err = m.Redeem(code, "a", "ios")
	require.NoError(t, err)
	_, _, err = m.Redeem(code, "b", "ios")
	assert.ErrorIs(t, err, ErrInvalidCode)
}

func TestConcurrentRedeemHasOneWinner(t *testing.T) {
	m, _ := newManager(t)
	code, _, err := m.CreateCode("u", time.Minute)
	require.NoError(t, err)

	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := m.Redeem(code, "d", "ios"); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), wins.Load())
	assert.Len(t, m.List(), 1)
}

func TestExpiredAndUnknownCodesAreRejected(t *testing.T) {
	m, _ := newManager(t)
	now := time.Now()
	m.now = func() time.Time { return now }
	code, _, err := m.CreateCode("u", time.Minute)
	require.NoError(t, err)

	m.now = func() time.Time { return now.Add(time.Minute) }
	_, _, err = m.Redeem(code, "d", "ios")
	assert.ErrorIs(t, err, ErrInvalidCode)

	_, _, err = m.Redeem("NOTACODE", "d", "ios")
	assert.ErrorIs(t, err, ErrInvalidCode)
}

func TestCreateCodeValidation(t *testing.T) {
	m, _ := newManager(t)
	_, _, err := m.CreateCode("", time.Minute)
	assert.Error(t, err)
	_, _, err = m.CreateCode("u", 0)
	assert.Error(t, err)
	_, _, err = m.CreateCode("\x01shared", time.Minute)
	assert.Error(t, err, "control characters are reserved")
}

func TestRevokeDisablesToken(t *testing.T) {
	m, _ := newManager(t)
	code, _, _ := m.CreateCode("u", time.Minute)
	d, token, err := m.Redeem(code, "d", "ios")
	require.NoError(t, err)

	revoked, err := m.Revoke(d.ID)
	require.NoError(t, err)
	assert.NotNil(t, revoked.RevokedAt)
	_, ok := m.Authenticate(token)
	assert.False(t, ok)

	_, err = m.Revoke("dev_missing")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestAuthenticateRejectsForeignTokens(t *testing.T) {
	m, _ := newManager(t)
	for _, tok := range []string{"", "eyJhbGciOi.jwt.token", TokenPrefix + "unknown"} {
		_, ok := m.Authenticate(tok)
		assert.False(t, ok, tok)
	}
}

func TestDevicesPersistAcrossRestart(t *testing.T) {
	m, path := newManager(t)
	code, _, _ := m.CreateCode("u", time.Minute)
	d, token, err := m.Redeem(code, "d", "ios")
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), token, "the token itself must never be stored")

	reopened, err := Open(path)
	require.NoError(t, err)
	got, ok := reopened.Authenticate(token)
	require.True(t, ok)
	assert.Equal(t, d.ID, got.ID)
}

func TestLastSeenIsThrottled(t *testing.T) {
	m, _ := newManager(t)
	now := time.Now()
	m.now = func() time.Time { return now }
	code, _, _ := m.CreateCode("u", time.Minute)
	_, token, err := m.Redeem(code, "d", "ios")
	require.NoError(t, err)

	m.now = func() time.Time { return now.Add(30 * time.Second) }
	d, _ := m.Authenticate(token)
	assert.Equal(t, now, d.LastSeenAt)

	m.now = func() time.Time { return now.Add(2 * time.Minute) }
	d, _ = m.Authenticate(token)
	assert.Equal(t, now.Add(2*time.Minute), d.LastSeenAt)
}

func TestAdminToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "device-admin.token")
	tok, err := LoadOrCreateAdminToken(path)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(tok, AdminTokenPrefix))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	again, err := LoadOrCreateAdminToken(path)
	require.NoError(t, err)
	assert.Equal(t, tok, again)

	read, err := ReadAdminToken(path)
	require.NoError(t, err)
	assert.True(t, TokensEqual(tok, read))
	assert.False(t, TokensEqual(tok, tok+"x"))

	require.NoError(t, os.WriteFile(path, []byte("garbage"), 0o600))
	_, err = LoadOrCreateAdminToken(path)
	assert.Error(t, err)
}
