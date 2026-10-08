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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// resetState swaps in a test-local caCertFilePath/initialized state and restores the
// package globals afterwards, since they are shared across tests in this package. Any
// watcher goroutine started during the test is stopped and awaited before the globals
// are restored, so it never observes a path mutated by a later test.
func resetState(t *testing.T, caCertPath string) {
	origPath := caCertFilePath
	origInitialized := initialized
	t.Cleanup(func() {
		if caCertWatcher != nil {
			_ = caCertWatcher.Close()
			<-watchDone
			caCertWatcher = nil
			watchDone = nil
		}
		caCertFilePath = origPath
		initialized = origInitialized
	})
	caCertFilePath = caCertPath
	initialized = false
}

func TestEnsureCACertWatcherStartedMissingCACert(t *testing.T) {
	dir := t.TempDir()
	resetState(t, filepath.Join(dir, "missing-ca-cert.pem"))

	var err error
	assert.NotPanics(t, func() {
		err = ensureCACertWatcherStarted()
	})
	assert.Error(t, err)
	assert.False(t, initialized)
}

func TestEnsureCACertWatcherStartedSuccess(t *testing.T) {
	dir := t.TempDir()
	caCertPath := filepath.Join(dir, "ca-cert.pem")
	assert.NoError(t, os.WriteFile(caCertPath, []byte("dummy-cert-content"), 0644))
	resetState(t, caCertPath)

	err := ensureCACertWatcherStarted()
	assert.NoError(t, err)
	assert.True(t, initialized)
	assert.NotNil(t, client)

	// A second call should be a no-op, since the watcher is already running.
	err = ensureCACertWatcherStarted()
	assert.NoError(t, err)
}
