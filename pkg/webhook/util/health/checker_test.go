/*
Copyright 2022 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package health

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// resetState resets package-level globals between tests.
func resetState() {
	initMu.Lock()
	if currentWatcher != nil {
		_ = currentWatcher.Close()
		currentWatcher = nil
	}
	initialized = false
	initMu.Unlock()
	lock.Lock()
	client = nil
	lock.Unlock()
}

// generateTestCACert creates a self-signed CA certificate PEM for testing.
func generateTestCACert(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
}

func TestCheckerMissingCertFile(t *testing.T) {
	resetState()
	orig := caCertFilePath
	caCertFilePath = filepath.Join(t.TempDir(), "nonexistent-ca-cert.pem")
	defer func() {
		caCertFilePath = orig
		resetState()
	}()

	err := Checker(nil)
	if err == nil {
		t.Fatal("expected error when cert file is missing, got nil")
	}
}

func TestCheckerRetryAfterFailure(t *testing.T) {
	resetState()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")

	orig := caCertFilePath
	caCertFilePath = certPath
	defer func() {
		caCertFilePath = orig
		resetState()
	}()

	// First call: cert does not exist yet → must return error, not panic.
	err := Checker(nil)
	if err == nil {
		t.Fatal("expected error on first call with missing cert")
	}
	initMu.Lock()
	initDone := initialized
	initMu.Unlock()
	if initDone {
		t.Fatal("initialized must be false after failed init attempt")
	}

	// Write the cert file so the next attempt can succeed.
	if err := os.WriteFile(certPath, generateTestCACert(t), 0644); err != nil {
		t.Fatalf("failed to write cert: %v", err)
	}

	// Second Checker() call: tryInit succeeds, then the HTTP request to localhost
	// fails with a connection error (no server running) — that's expected.
	// The important thing is that tryInit completed successfully.
	_ = Checker(nil)
	initMu.Lock()
	initDone = initialized
	initMu.Unlock()
	if !initDone {
		t.Fatal("initialized must be true after successful init via Checker")
	}
}

func TestTryInitFastPath(t *testing.T) {
	resetState()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")

	orig := caCertFilePath
	caCertFilePath = certPath
	defer func() {
		caCertFilePath = orig
		resetState()
	}()

	if err := os.WriteFile(certPath, generateTestCACert(t), 0644); err != nil {
		t.Fatalf("failed to write cert: %v", err)
	}

	// First call: performs full initialization.
	if err := tryInit(); err != nil {
		t.Fatalf("first tryInit failed: %v", err)
	}

	// Second call: must hit the fast path (initialized == true) and return nil.
	if err := tryInit(); err != nil {
		t.Fatalf("second tryInit (fast path) failed: %v", err)
	}
}

func TestIsWriteCreateRemove(t *testing.T) {
	writeEvent := fsnotify.Event{Op: fsnotify.Write}
	createEvent := fsnotify.Event{Op: fsnotify.Create}
	removeEvent := fsnotify.Event{Op: fsnotify.Remove}
	noneEvent := fsnotify.Event{Op: fsnotify.Chmod}

	if !isWrite(writeEvent) {
		t.Error("expected isWrite to return true for Write event")
	}
	if isWrite(createEvent) {
		t.Error("expected isWrite to return false for Create event")
	}
	if !isCreate(createEvent) {
		t.Error("expected isCreate to return true for Create event")
	}
	if isCreate(writeEvent) {
		t.Error("expected isCreate to return false for Write event")
	}
	if !isRemove(removeEvent) {
		t.Error("expected isRemove to return true for Remove event")
	}
	if isRemove(noneEvent) {
		t.Error("expected isRemove to return false for Chmod event")
	}
}

func TestLoadHTTPClientWithCACert(t *testing.T) {
	resetState()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")

	orig := caCertFilePath
	caCertFilePath = certPath
	defer func() {
		caCertFilePath = orig
		resetState()
	}()

	if err := os.WriteFile(certPath, generateTestCACert(t), 0644); err != nil {
		t.Fatalf("failed to write cert: %v", err)
	}

	if err := loadHTTPClientWithCACert(); err != nil {
		t.Fatalf("expected no error loading CA cert, got: %v", err)
	}

	lock.Lock()
	c := client
	lock.Unlock()

	if c == nil {
		t.Fatal("expected client to be set after loadHTTPClientWithCACert")
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatal("expected TLS config with RootCAs to be set on client transport")
	}
}
