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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Pinned TLS: the HTTP gateway serves HTTPS with a self-signed certificate
// that is made once and kept, and pairing links carry its SHA-256 (the
// pin). A paired device trusts exactly that certificate, with no
// certificate authority and no host name check, so traffic on a home
// network is encrypted and nobody else on it can pose as the server.
// See docs/architecture/device-pairing.md.

// Pinned TLS file names in the data directory.
const (
	PinnedCertFileName = "gateway-tls.crt"
	PinnedKeyFileName  = "gateway-tls.key"
)

// pinnedCertValidity is long because devices check the pin, not dates;
// replacing the certificate means pairing every device again.
const pinnedCertValidity = 20 * 365 * 24 * time.Hour

// PinnedCert is the gateway's certificate and its pin.
type PinnedCert struct {
	Certificate tls.Certificate
	// Pin is the unpadded base64url SHA-256 of the certificate's DER bytes.
	Pin string
	// CertFile is where the certificate is kept (PEM), for local clients
	// that trust it as a root (e.g. a setup app's health check).
	CertFile string
}

// CertPin returns the pin of a DER-encoded certificate.
func CertPin(der []byte) string {
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// LoadOrCreatePinnedCert returns the certificate kept in dir, making it
// (ECDSA P-256, SHA-256, owner-only files) the first time. hosts are the
// names and addresses it lists; they matter only to clients that do not
// pin (devices pin and ignore them).
func LoadOrCreatePinnedCert(dir string, hosts []string) (*PinnedCert, error) {
	certPath := filepath.Join(dir, PinnedCertFileName)
	keyPath := filepath.Join(dir, PinnedKeyFileName)
	certPEM, certErr := os.ReadFile(certPath) // #nosec G304 -- path in the server's data directory
	keyPEM, keyErr := os.ReadFile(keyPath)    // #nosec G304 -- path in the server's data directory
	switch {
	case certErr == nil && keyErr == nil:
		return parsePinned(certPEM, keyPEM, certPath)
	case errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
	case certErr != nil && !errors.Is(certErr, os.ErrNotExist):
		return nil, fmt.Errorf("read %s: %w", certPath, certErr)
	case keyErr != nil && !errors.Is(keyErr, os.ErrNotExist):
		return nil, fmt.Errorf("read %s: %w", keyPath, keyErr)
	default:
		return nil, fmt.Errorf("only one of %s and %s exists; remove it to make a new certificate (devices must then be paired again)", certPath, keyPath)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Loom assistant"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(pinnedCertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	// The key first: a crash between the two writes leaves only the key,
	// which the "only one exists" case reports instead of reusing it.
	if err := writeFileAtomic(keyPath, keyPEM); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(certPath, certPEM); err != nil {
		return nil, err
	}
	return parsePinned(certPEM, keyPEM, certPath)
}

func parsePinned(certPEM, keyPEM []byte, certPath string) (*PinnedCert, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("read pinned certificate: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, errors.New("read pinned certificate: no certificate")
	}
	return &PinnedCert{Certificate: pair, Pin: CertPin(pair.Certificate[0]), CertFile: certPath}, nil
}

// writeFileAtomic writes data to path with owner-only permissions through
// a temporary file and a rename.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
