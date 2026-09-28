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

package system

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSupportedIfWritableInKubepods(t *testing.T) {
	// pin the cgroup driver so the kubepods parent dir name is deterministic
	origFormatter := CgroupPathFormatter
	SetupCgroupPathFormatter(Cgroupfs)
	defer func() { CgroupPathFormatter = origFormatter }()

	newTestResource := func() Resource {
		return DefaultFactory.New("memory.my_test_cgroup", CgroupMemDir).
			WithCheckSupported(SupportedIfWritableInKubepods)
	}

	t.Run("file not exist in kubepods", func(t *testing.T) {
		testHelper := NewFileTestUtil(t)
		defer testHelper.Cleanup()

		r := newTestResource()
		supported, msg := r.IsSupported("")
		assert.False(t, supported)
		assert.Equal(t, "file not exist in kubepods cgroup", msg)
	})

	t.Run("file exists and writable", func(t *testing.T) {
		testHelper := NewFileTestUtil(t)
		defer testHelper.Cleanup()

		r := newTestResource()
		filePath := r.Path(CgroupPathFormatter.ParentDir)
		testHelper.MkDirAll(filepath.Dir(filePath))
		content := "12345\n"
		testHelper.WriteFileContents(filePath, content)

		supported, msg := r.IsSupported("")
		assert.True(t, supported)
		assert.Empty(t, msg)
		// the probe writes back the exact content it read, so the file must be unchanged
		assert.Equal(t, content, testHelper.ReadFileContents(filePath))
	})

	t.Run("file exists but not writable after successful probe", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("running as root, the read-only permission check is ineffective")
		}
		testHelper := NewFileTestUtil(t)
		defer testHelper.Cleanup()

		r := newTestResource()
		filePath := r.Path(CgroupPathFormatter.ParentDir)
		testHelper.MkDirAll(filepath.Dir(filePath))
		testHelper.WriteFileContents(filePath, "12345\n")

		// the check must not be check-once: a later kernel change is picked up by re-probing
		supported, msg := r.IsSupported("")
		assert.True(t, supported)
		assert.Empty(t, msg)

		assert.NoError(t, os.Chmod(filePath, 0444))
		supported, msg = r.IsSupported("")
		assert.False(t, supported)
		assert.Contains(t, msg, "write probe failed")
	})
}
