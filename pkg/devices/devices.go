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

// Package devices pairs client devices with a self-hosted server. An operator
// issues a short-lived single-use pairing code; a device redeems it for a
// long-lived device token, which then authenticates its requests.
//
// Only hashes of codes and tokens are kept. Pairing codes live in memory (a
// restart invalidates outstanding codes); devices persist to a JSON file, so
// this store serves a single server process. See
// docs/architecture/device-pairing.md.
package devices

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// TokenPrefix marks device tokens so authenticators can tell them apart
	// from other bearer credentials (e.g. JWTs) without a lookup.
	TokenPrefix = "loomd_"

	// AdminTokenPrefix marks the admin token.
	AdminTokenPrefix = "looma_"

	// MaxDeviceNameLength bounds the device name stored per device.
	MaxDeviceNameLength = 100

	// lastSeenResolution limits how often LastSeenAt is persisted per device.
	lastSeenResolution = time.Minute
)

// ErrInvalidCode is returned for unknown, used, or expired pairing codes. The
// cases are deliberately indistinguishable to callers.
var ErrInvalidCode = errors.New("invalid or expired pairing code")

// ErrNotFound is returned when a device id does not exist.
var ErrNotFound = errors.New("device not found")

// Device is a paired device. TokenHash is the hex SHA-256 of its token.
type Device struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	UserID     string     `json:"user_id"`
	TokenHash  string     `json:"token_hash"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type pendingCode struct {
	userID    string
	expiresAt time.Time
}

// Manager issues pairing codes, redeems them, and authenticates device tokens.
// It is safe for concurrent use.
type Manager struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	codes   map[string]pendingCode // keyed by code hash
	devices []*Device
	byHash  map[string]*Device
}

// Open loads (or creates) the device file at path.
func Open(path string) (*Manager, error) {
	m := &Manager{
		path:   path,
		now:    time.Now,
		codes:  make(map[string]pendingCode),
		byHash: make(map[string]*Device),
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return m, nil
	case err != nil:
		return nil, fmt.Errorf("read devices file: %w", err)
	}
	var stored struct {
		Devices []*Device `json:"devices"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse devices file %s: %w", path, err)
	}
	for _, d := range stored.Devices {
		m.devices = append(m.devices, d)
		m.byHash[d.TokenHash] = d
	}
	return m, nil
}

// CreateCode issues a single-use pairing code for userID, valid for ttl.
func (m *Manager) CreateCode(userID string, ttl time.Duration) (string, time.Time, error) {
	if userID == "" {
		return "", time.Time{}, errors.New("user id is required")
	}
	if ttl <= 0 {
		return "", time.Time{}, errors.New("ttl must be positive")
	}
	raw, err := randomBytes(20)
	if err != nil {
		return "", time.Time{}, err
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)

	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.pruneCodesLocked(now)
	expires := now.Add(ttl)
	m.codes[hash(code)] = pendingCode{userID: userID, expiresAt: expires}
	return code, expires, nil
}

// Redeem consumes code and registers a device, returning it and its token.
// The token is not stored and cannot be retrieved again.
func (m *Manager) Redeem(code, name, platform string) (*Device, string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	name = truncate(strings.TrimSpace(name), MaxDeviceNameLength)
	platform = truncate(strings.TrimSpace(platform), 32)

	rawToken, err := randomBytes(32)
	if err != nil {
		return nil, "", err
	}
	token := TokenPrefix + base64.RawURLEncoding.EncodeToString(rawToken)
	rawID, err := randomBytes(8)
	if err != nil {
		return nil, "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	key := hash(code)
	pending, ok := m.codes[key]
	if !ok || !now.Before(pending.expiresAt) {
		delete(m.codes, key)
		return nil, "", ErrInvalidCode
	}
	delete(m.codes, key)

	d := &Device{
		ID:         "dev_" + hex.EncodeToString(rawID),
		Name:       name,
		Platform:   platform,
		UserID:     pending.userID,
		TokenHash:  hash(token),
		CreatedAt:  now,
		LastSeenAt: now,
	}
	m.devices = append(m.devices, d)
	m.byHash[d.TokenHash] = d
	if err := m.saveLocked(); err != nil {
		m.devices = m.devices[:len(m.devices)-1]
		delete(m.byHash, d.TokenHash)
		return nil, "", err
	}
	cp := *d
	return &cp, token, nil
}

// Authenticate returns the device for token, or false when the token is
// unknown or revoked.
func (m *Manager) Authenticate(token string) (*Device, bool) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.byHash[hash(token)]
	if !ok || d.RevokedAt != nil {
		return nil, false
	}
	now := m.now()
	if now.Sub(d.LastSeenAt) >= lastSeenResolution {
		d.LastSeenAt = now
		// Best effort: a failed write only loses the timestamp update.
		_ = m.saveLocked()
	}
	cp := *d
	return &cp, true
}

// List returns all devices, oldest first.
func (m *Manager) List() []Device {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Device, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, *d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Revoke disables a device. Revoking a revoked device is a no-op.
func (m *Manager) Revoke(id string) (*Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.devices {
		if d.ID != id {
			continue
		}
		if d.RevokedAt == nil {
			now := m.now()
			d.RevokedAt = &now
			if err := m.saveLocked(); err != nil {
				d.RevokedAt = nil
				return nil, err
			}
		}
		cp := *d
		return &cp, nil
	}
	return nil, ErrNotFound
}

func (m *Manager) pruneCodesLocked(now time.Time) {
	for k, c := range m.codes {
		if !now.Before(c.expiresAt) {
			delete(m.codes, k)
		}
	}
}

// saveLocked writes the device file atomically with owner-only permissions.
func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(struct {
		Devices []*Device `json:"devices"`
	}{m.devices}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return fmt.Errorf("create devices dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".devices-*.json")
	if err != nil {
		return fmt.Errorf("write devices file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write devices file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write devices file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write devices file: %w", err)
	}
	if err := os.Rename(tmp.Name(), m.path); err != nil {
		return fmt.Errorf("write devices file: %w", err)
	}
	return nil
}

// LoadOrCreateAdminToken returns the admin token stored at path, creating it
// with owner-only permissions when missing. The admin token authorizes
// issuing pairing codes and managing devices; anyone who can read the file
// can pair devices.
func LoadOrCreateAdminToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if !strings.HasPrefix(token, AdminTokenPrefix) {
			return "", fmt.Errorf("admin token file %s is malformed", path)
		}
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read admin token: %w", err)
	}
	raw, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	token := AdminTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create admin token dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create admin token: %w", err)
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write admin token: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write admin token: %w", err)
	}
	return token, nil
}

// ReadAdminToken reads an existing admin token (for CLI clients).
func ReadAdminToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// TokensEqual compares two secrets in constant time.
func TokensEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate random bytes: %w", err)
	}
	return b, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
