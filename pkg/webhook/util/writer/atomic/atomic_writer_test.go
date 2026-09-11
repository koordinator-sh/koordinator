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

package atomic

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewAtomicWriter(t *testing.T) {
	t.Run("existing directory", func(t *testing.T) {
		dir := t.TempDir()
		w, err := NewAtomicWriter(dir)
		assert.NoError(t, err)
		assert.NotNil(t, w)
		assert.Equal(t, dir, w.targetDir)
	})

	t.Run("non-existent directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "does-not-exist")
		w, err := NewAtomicWriter(dir)
		assert.Error(t, err)
		assert.True(t, os.IsNotExist(err))
		assert.Nil(t, w)
	})
}

func TestValidatePath(t *testing.T) {
	longFileName := strings.Repeat("a", maxFileNameLength+1)
	longPath := strings.Repeat("a/", (maxPathLength/2)+1)

	tests := []struct {
		name      string
		path      string
		wantError bool
	}{
		{
			name:      "empty path",
			path:      "",
			wantError: true,
		},
		{
			name:      "absolute path",
			path:      "/etc/passwd",
			wantError: true,
		},
		{
			name:      "path containing '..' element",
			path:      "foo/../bar",
			wantError: true,
		},
		{
			name:      "single component equal to '..'",
			path:      "..",
			wantError: true,
		},
		{
			name:      "path starting with '..foo'",
			path:      "..foo/bar",
			wantError: true,
		},
		{
			name:      "filename longer than max",
			path:      longFileName,
			wantError: true,
		},
		{
			name:      "path longer than max",
			path:      longPath,
			wantError: true,
		},
		{
			name:      "valid simple relative path",
			path:      "tls.crt",
			wantError: false,
		},
		{
			name:      "valid nested relative path",
			path:      "ca/bundle.crt",
			wantError: false,
		},
		{
			name:      "valid hidden dotfile",
			path:      ".dotfile",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePayload(t *testing.T) {
	t.Run("cleans paths", func(t *testing.T) {
		payload := map[string]FileProjection{
			"foo/./bar": {Data: []byte("data"), Mode: 0644},
		}
		clean, err := validatePayload(payload)
		assert.NoError(t, err)
		_, ok := clean["foo/bar"]
		assert.True(t, ok, "expected cleaned key foo/bar, got: %v", clean)
	})

	t.Run("returns error on invalid path", func(t *testing.T) {
		payload := map[string]FileProjection{
			"../escape": {Data: []byte("data"), Mode: 0644},
		}
		clean, err := validatePayload(payload)
		assert.Error(t, err)
		assert.Nil(t, clean)
	})
}

func TestShouldWriteFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	assert.NoError(t, os.WriteFile(existing, []byte("hello"), 0644))

	t.Run("missing file should write", func(t *testing.T) {
		should, err := shouldWriteFile(filepath.Join(dir, "missing"), []byte("any"))
		assert.NoError(t, err)
		assert.True(t, should)
	})

	t.Run("identical content should not write", func(t *testing.T) {
		should, err := shouldWriteFile(existing, []byte("hello"))
		assert.NoError(t, err)
		assert.False(t, should)
	})

	t.Run("different content should write", func(t *testing.T) {
		should, err := shouldWriteFile(existing, []byte("world"))
		assert.NoError(t, err)
		assert.True(t, should)
	})
}

func TestShouldWritePayload(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "tls.crt"), []byte("cert"), 0644))

	t.Run("no change required", func(t *testing.T) {
		payload := map[string]FileProjection{
			"tls.crt": {Data: []byte("cert"), Mode: 0644},
		}
		should, err := shouldWritePayload(payload, dir)
		assert.NoError(t, err)
		assert.False(t, should)
	})

	t.Run("change required", func(t *testing.T) {
		payload := map[string]FileProjection{
			"tls.crt": {Data: []byte("new-cert"), Mode: 0644},
		}
		should, err := shouldWritePayload(payload, dir)
		assert.NoError(t, err)
		assert.True(t, should)
	})

	t.Run("new file requires write", func(t *testing.T) {
		payload := map[string]FileProjection{
			"tls.key": {Data: []byte("key"), Mode: 0644},
		}
		should, err := shouldWritePayload(payload, dir)
		assert.NoError(t, err)
		assert.True(t, should)
	})
}

// readVisible reads the content of a user-visible file via the target dir
// (i.e. through the ..data symlink chain).
func readVisible(t *testing.T, targetDir, name string) ([]byte, error) {
	t.Helper()
	return os.ReadFile(path.Join(targetDir, name))
}

func dataDirTarget(t *testing.T, targetDir string) string {
	t.Helper()
	target, err := os.Readlink(path.Join(targetDir, dataDirName))
	assert.NoError(t, err)
	return target
}

func TestWrite_FirstWrite(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAtomicWriter(dir)
	assert.NoError(t, err)

	payload := map[string]FileProjection{
		"tls.crt": {Data: []byte("cert-data"), Mode: 0644},
		"tls.key": {Data: []byte("key-data"), Mode: 0644},
	}
	assert.NoError(t, w.Write(payload))

	// ..data symlink and timestamped dir exist
	tsDir := dataDirTarget(t, dir)
	assert.True(t, strings.HasPrefix(tsDir, "..20"), "unexpected ts dir name: %s", tsDir)

	// user-visible symlinks resolve to the projected content
	crt, err := readVisible(t, dir, "tls.crt")
	assert.NoError(t, err)
	assert.Equal(t, []byte("cert-data"), crt)

	key, err := readVisible(t, dir, "tls.key")
	assert.NoError(t, err)
	assert.Equal(t, []byte("key-data"), key)

	// the visible files are actually symlinks
	info, err := os.Lstat(path.Join(dir, "tls.crt"))
	assert.NoError(t, err)
	assert.Equal(t, os.ModeSymlink, info.Mode()&os.ModeSymlink)
}

func TestWrite_NoOpWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAtomicWriter(dir)
	assert.NoError(t, err)

	payload := map[string]FileProjection{
		"tls.crt": {Data: []byte("cert-data"), Mode: 0644},
	}
	assert.NoError(t, w.Write(payload))
	firstTs := dataDirTarget(t, dir)

	// Writing the same payload again must not rotate the timestamped dir.
	assert.NoError(t, w.Write(payload))
	secondTs := dataDirTarget(t, dir)
	assert.Equal(t, firstTs, secondTs, "timestamped dir should not rotate for an unchanged payload")
}

func TestWrite_UpdatesChangedData(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAtomicWriter(dir)
	assert.NoError(t, err)

	assert.NoError(t, w.Write(map[string]FileProjection{
		"tls.crt": {Data: []byte("v1"), Mode: 0644},
	}))
	firstTs := dataDirTarget(t, dir)

	assert.NoError(t, w.Write(map[string]FileProjection{
		"tls.crt": {Data: []byte("v2"), Mode: 0644},
	}))
	secondTs := dataDirTarget(t, dir)

	assert.NotEqual(t, firstTs, secondTs, "timestamped dir should rotate when data changes")

	crt, err := readVisible(t, dir, "tls.crt")
	assert.NoError(t, err)
	assert.Equal(t, []byte("v2"), crt)

	// old timestamped dir must be removed
	_, err = os.Stat(path.Join(dir, firstTs))
	assert.True(t, os.IsNotExist(err), "old timestamped dir should be removed")
}

func TestWrite_RemovesDeletedPaths(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAtomicWriter(dir)
	assert.NoError(t, err)

	assert.NoError(t, w.Write(map[string]FileProjection{
		"tls.crt": {Data: []byte("cert"), Mode: 0644},
		"tls.key": {Data: []byte("key"), Mode: 0644},
	}))

	// Drop tls.key from the payload.
	assert.NoError(t, w.Write(map[string]FileProjection{
		"tls.crt": {Data: []byte("cert"), Mode: 0644},
	}))

	// tls.key's visible symlink must be gone.
	_, err = os.Lstat(path.Join(dir, "tls.key"))
	assert.True(t, os.IsNotExist(err), "tls.key symlink should be removed")
	_, err = readVisible(t, dir, "tls.key")
	assert.Error(t, err)

	// tls.crt must still be readable.
	crt, err := readVisible(t, dir, "tls.crt")
	assert.NoError(t, err)
	assert.Equal(t, []byte("cert"), crt)
}

func TestWrite_InvalidPayloadDoesNotTouchFS(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "empty path", key: ""},
		{name: "absolute path", key: "/abs"},
		{name: "contains '..'", key: "foo/../bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			w, err := NewAtomicWriter(dir)
			assert.NoError(t, err)

			err = w.Write(map[string]FileProjection{
				tt.key: {Data: []byte("data"), Mode: 0644},
			})
			assert.Error(t, err)

			// Nothing should have been projected.
			_, err = os.Readlink(path.Join(dir, dataDirName))
			assert.True(t, os.IsNotExist(err), "..data symlink should not exist after invalid write")

			entries, err := os.ReadDir(dir)
			assert.NoError(t, err)
			assert.Empty(t, entries, "target dir should remain empty after invalid write")
		})
	}
}

func TestWrite_RecreatesAfterDataDirRemoved(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAtomicWriter(dir)
	assert.NoError(t, err)

	payload := map[string]FileProjection{
		"tls.crt": {Data: []byte("cert"), Mode: 0644},
	}
	assert.NoError(t, w.Write(payload))

	// Simulate the data dir disappearing.
	assert.NoError(t, os.RemoveAll(path.Join(dir, dataDirName)))

	// A subsequent write must succeed and recreate the projection.
	assert.NoError(t, w.Write(payload))
	crt, err := readVisible(t, dir, "tls.crt")
	assert.NoError(t, err)
	assert.Equal(t, []byte("cert"), crt)
}

func TestWrite_NestedPath(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAtomicWriter(dir)
	assert.NoError(t, err)

	assert.NoError(t, w.Write(map[string]FileProjection{
		"ca/bundle.crt": {Data: []byte("bundle"), Mode: 0644},
	}))

	// The user-visible "ca" entry must be a symlink into ..data.
	info, err := os.Lstat(path.Join(dir, "ca"))
	assert.NoError(t, err)
	assert.Equal(t, os.ModeSymlink, info.Mode()&os.ModeSymlink)

	bundle, err := readVisible(t, dir, "ca/bundle.crt")
	assert.NoError(t, err)
	assert.Equal(t, []byte("bundle"), bundle)

	// The file is stored under the timestamped data dir.
	tsDir := dataDirTarget(t, dir)
	stored, err := os.ReadFile(path.Join(dir, tsDir, "ca", "bundle.crt"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("bundle"), stored)
}

func TestWrite_FileModePreserved(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAtomicWriter(dir)
	assert.NoError(t, err)

	assert.NoError(t, w.Write(map[string]FileProjection{
		"tls.key": {Data: []byte("key"), Mode: 0600},
	}))

	tsDir := dataDirTarget(t, dir)
	info, err := os.Stat(path.Join(dir, tsDir, "tls.key"))
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestPathsToRemove(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAtomicWriter(dir)
	assert.NoError(t, err)

	// Lay down an "old" timestamped dir with two files.
	oldTs := filepath.Join(dir, "..old")
	assert.NoError(t, os.MkdirAll(filepath.Join(oldTs, "sub"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(oldTs, "keep"), []byte("a"), 0644))
	assert.NoError(t, os.WriteFile(filepath.Join(oldTs, "sub", "drop"), []byte("b"), 0644))

	payload := map[string]FileProjection{
		"keep": {Data: []byte("a"), Mode: 0644},
	}

	toRemove, err := w.pathsToRemove(payload, oldTs)
	assert.NoError(t, err)
	assert.True(t, toRemove.Has(filepath.Join("sub", "drop")), "sub/drop should be scheduled for removal")
	assert.False(t, toRemove.Has("keep"), "keep is still in the payload and must not be removed")
}

func TestNewTimestampDir(t *testing.T) {
	dir := t.TempDir()
	w, err := NewAtomicWriter(dir)
	assert.NoError(t, err)

	tsDir, err := w.newTimestampDir()
	assert.NoError(t, err)

	info, err := os.Stat(tsDir)
	assert.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
	assert.True(t, strings.HasPrefix(filepath.Base(tsDir), "..20"), "unexpected ts dir name: %s", tsDir)
}
