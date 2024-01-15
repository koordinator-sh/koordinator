//go:build !linux
// +build !linux

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

func Test_MountResctrlSubsystem(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		sysFSRootDir := t.TempDir()
		resctrlDir := filepath.Join(sysFSRootDir, ResctrlDir)
		err := os.MkdirAll(resctrlDir, 0700)
		assert.NoError(t, err)

		schemataPath := filepath.Join(resctrlDir, ResctrlSchemataName)
		err = os.WriteFile(schemataPath, []byte("    L3:0=ff;1=ff\n    MB:0=100;1=100\n"), 0666)
		assert.NoError(t, err)

		Conf = &Config{
			SysFSRootDir: sysFSRootDir,
		}

		got, err := MountResctrlSubsystem()

		// resctrl is only supported by linux
		assert.Equal(t, false, got)
		assert.EqualError(t, err, "only support linux")
		return
	})
}
