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
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPinnedCertIsMadeOnceAndKept(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreatePinnedCert(dir, []string{"localhost", "127.0.0.1", "192.168.1.20"})
	require.NoError(t, err)
	assert.Len(t, first.Pin, 43, "unpadded base64url of 32 bytes")

	sum := sha256.Sum256(first.Certificate.Certificate[0])
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), first.Pin)

	again, err := LoadOrCreatePinnedCert(dir, []string{"other"})
	require.NoError(t, err)
	assert.Equal(t, first.Pin, again.Pin, "the same certificate on every start")

	for _, name := range []string{PinnedCertFileName, PinnedKeyFileName} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), name)
	}

	leaf, err := x509.ParseCertificate(first.Certificate.Certificate[0])
	require.NoError(t, err)
	assert.Equal(t, x509.ECDSAWithSHA256, leaf.SignatureAlgorithm)
	assert.Contains(t, leaf.DNSNames, "localhost")
	assert.Len(t, leaf.IPAddresses, 2)
}

func TestPinnedCertRefusesHalfAPair(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadOrCreatePinnedCert(dir, nil)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, PinnedCertFileName)))
	_, err = LoadOrCreatePinnedCert(dir, nil)
	assert.ErrorContains(t, err, "only one of")
}

// A client that trusts the certificate file as a root reaches the server at
// a name it lists (how the setup app checks health).
func TestPinnedCertServesTLS(t *testing.T) {
	dir := t.TempDir()
	pc, err := LoadOrCreatePinnedCert(dir, []string{"127.0.0.1"})
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pc.Certificate}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()

	pemBytes, err := os.ReadFile(pc.CertFile)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(pemBytes))
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	resp, err := client.Get(srv.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// And a pinning client sees the pin in the handshake.
	conn, err := tls.Dial("tcp", srv.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) // #nosec G402 -- the test checks the pin itself
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	assert.Equal(t, pc.Pin, CertPin(conn.ConnectionState().PeerCertificates[0].Raw))
}
