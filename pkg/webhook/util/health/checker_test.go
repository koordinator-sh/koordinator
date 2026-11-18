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
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// useCACertPath points the checker at path and resets its state around the test.
func useCACertPath(t *testing.T, path string) {
	t.Helper()
	origPath := caCertFilePath
	reset := func() {
		initMu.Lock()
		initialized = false
		initMu.Unlock()
		lock.Lock()
		client = nil
		lock.Unlock()
	}
	caCertFilePath = path
	reset()
	t.Cleanup(func() {
		caCertFilePath = origPath
		reset()
	})
}

func TestCheckerReturnsErrorWhenCACertIsMissing(t *testing.T) {
	useCACertPath(t, filepath.Join(t.TempDir(), "ca-cert.pem"))

	var err error
	assert.NotPanics(t, func() { err = Checker(nil) })
	assert.ErrorContains(t, err, "failed to load ca-cert for the first time")
	assert.False(t, initialized)
}

func TestCheckerRetriesInitAfterCACertAppears(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "ca-cert.pem")
	useCACertPath(t, certPath)

	assert.Error(t, Checker(nil))
	assert.False(t, initialized)

	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	assert.NoError(t, os.WriteFile(certPath, certPEM, 0o600))

	// Init now succeeds. The request itself still fails, because nothing listens on the webhook port.
	_ = Checker(nil)
	assert.True(t, initialized)
}
