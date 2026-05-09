package health

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// helper to create a CA and server cert signed by the CA
func makeCertAndServer(t *testing.T) (caPEM []byte, serverCert tls.Certificate, port int) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour * 24),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	// server key/cert
	srvKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour * 24),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		DNSNames:     []string{"localhost"},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caTmpl, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(srvKey)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}

	// Preserve the existing return signature for callers, but avoid probing
	// the OS for an ephemeral port when the tests do not use it.
	return caPEM, cert, port
}

func TestChecker_FallbackOnMissingCA_NoPanic(t *testing.T) {
	resetState(t)
	tmp := t.TempDir()
	setCertDir(t, tmp)
	t.Setenv("WEBHOOK_PORT", "0")

	// first call: no ca-cert present -> should return an error but not panic
	if err := Checker(nil); err == nil {
		t.Fatalf("expected error when no CA available, got nil")
	}

	// second call should attempt a retry path and still not panic
	if err := Checker(nil); err == nil {
		t.Fatalf("expected error on retry when no CA available, got nil")
	}
}

func TestChecker_WithCA_ServerOK(t *testing.T) {
	resetState(t)
	tmp := t.TempDir()
	setCertDir(t, tmp)

	caPEM, cert, _ := makeCertAndServer(t)

	// write ca cert
	caPath := filepath.Join(tmp, "ca-cert.pem")
	if err := os.WriteFile(caPath, caPEM, 0644); err != nil {
		t.Fatal(err)
	}

	// start TLS server using the cert
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := net.SplitHostPort(u.Host)
	_ = host
	t.Setenv("WEBHOOK_PORT", portStr)

	// Now Checker should succeed (no panic) when CA is present
	if err := Checker(nil); err != nil {
		t.Fatalf("expected Checker to succeed, got error: %v", err)
	}
}

func resetState(t *testing.T) {
	t.Helper()
	stopWatch()
	// Reset once and client for test isolation
	onceWatch = sync.Once{}
	lock = sync.Mutex{}
	client = nil
	// restore newWatcher
	newWatcher = fsnotify.NewWatcher
	// Ensure watcher goroutines stop after each test
	t.Cleanup(stopWatch)
}

func stopWatch() {
	if watchCancel != nil {
		watchCancel()
	}
	if watchDone != nil {
		<-watchDone
	}
	watchCancel = nil
	watchDone = nil
}

func setCertDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("WEBHOOK_CERT_DIR", dir)
	setCertFilePath(t, filepath.Join(dir, "ca-cert.pem"))
}

func setCertFilePath(t *testing.T, filePath string) {
	t.Helper()
	old := caCertFilePath
	caCertFilePath = filePath
	t.Cleanup(func() {
		caCertFilePath = old
	})
}

func TestChecker_NewWatcherFailure_NoPanic(t *testing.T) {
	resetState(t)
	tmp := t.TempDir()
	setCertDir(t, tmp)
	t.Setenv("WEBHOOK_PORT", "0")

	// override newWatcher to simulate failure
	newWatcher = func() (*fsnotify.Watcher, error) {
		return nil, fmt.Errorf("simulated newwatcher failure")
	}

	if err := Checker(nil); err == nil {
		t.Fatalf("expected error when newWatcher fails, got nil")
	}
}

func TestChecker_WatcherAddFailure_NoPanic(t *testing.T) {
	resetState(t)
	// point caCertFilePath to a non-existent directory to cause watcher.Add to fail
	tmp := t.TempDir()
	badPath := filepath.Join(tmp, "nonexistent", "ca-cert.pem")
	setCertDir(t, tmp)
	setCertFilePath(t, badPath)
	t.Setenv("WEBHOOK_PORT", "0")

	if err := Checker(nil); err == nil {
		t.Fatalf("expected error when watcher.Add fails, got nil")
	}
}

func TestChecker_RetryLoadAfterFallback_Succeeds(t *testing.T) {
	resetState(t)
	tmp := t.TempDir()
	setCertDir(t, tmp)

	// start TLS server first
	caPEM, cert, _ := makeCertAndServer(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, _ := net.SplitHostPort(u.Host)
	t.Setenv("WEBHOOK_PORT", portStr)

	// First call: no CA on disk -> should error but set shared client to default
	if err := Checker(nil); err == nil {
		t.Fatalf("expected initial Checker to fail without CA, got nil")
	}

	// Write the CA file so subsequent retry can load it
	caPath := filepath.Join(tmp, "ca-cert.pem")
	if err := os.WriteFile(caPath, caPEM, 0644); err != nil {
		t.Fatal(err)
	}

	// Second call: should retry loading CA and succeed
	if err := Checker(nil); err != nil {
		t.Fatalf("expected Checker to succeed after CA present, got: %v", err)
	}
}
