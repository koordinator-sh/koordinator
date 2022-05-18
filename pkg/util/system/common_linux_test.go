//go:build linux
// +build linux

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
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ProcCmdLine(t *testing.T) {
	t.Run("testing process cmdline args should match", func(t *testing.T) {
		cmdline, err := ProcCmdLine("/proc", os.Getpid())
		assert.Empty(t, err)
		assert.ElementsMatch(t, cmdline, os.Args)
	})
	t.Run("fake process should fail", func(t *testing.T) {
		procRoot := t.TempDir()
		fakePid := 42
		fakeProcDir := filepath.Join(procRoot, strconv.Itoa((fakePid)))
		os.MkdirAll(fakeProcDir, 0555)

		_, err := ProcCmdLine(procRoot, fakePid)
		assert.NotEmpty(t, err)
	})
}

func Test_PidOf(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skipf("not supported on GOOS=%s", runtime.GOOS)
	}
	t.Run("testing process pid should match", func(t *testing.T) {
		pids, err := PidOf("/proc", filepath.Base(os.Args[0]))
		assert.Empty(t, err)
		assert.NotZero(t, pids)
		assert.Contains(t, pids, os.Getpid())
	})
	t.Run("empty process name should failed", func(t *testing.T) {
		_, err := PidOf("/proc", "")
		assert.Error(t, err)
	})
}
