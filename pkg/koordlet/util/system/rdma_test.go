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

func TestGetRDMAMinor(t *testing.T) {
	tests := []struct {
		name       string
		rdmaDevice string
		want       int32
		wantErr    bool
	}{
		{
			name:       "valid device",
			rdmaDevice: "mlx5_1",
			want:       1,
			wantErr:    false,
		},
		{
			name:       "valid device multiple digits",
			rdmaDevice: "mlx5_10",
			want:       10,
			wantErr:    false,
		},
		{
			name:       "invalid device missing underscore",
			rdmaDevice: "mlx51",
			want:       -1,
			wantErr:    true,
		},
		{
			name:       "invalid device wrong prefix",
			rdmaDevice: "eth0_1",
			want:       -1,
			wantErr:    true,
		},
		{
			name:       "invalid device empty",
			rdmaDevice: "",
			want:       -1,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetRDMAMinor(tt.rdmaDevice)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsRDMADeviceHealthy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "infiniband-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	origSysInfinibandDir := SysInfinibandDir
	SysInfinibandDir = tempDir
	defer func() {
		SysInfinibandDir = origSysInfinibandDir
	}()

	rdmaResource := "mlx5_test"
	portsPath := filepath.Join(tempDir, rdmaResource, "ports")

	port1Path := filepath.Join(portsPath, "1")
	err = os.MkdirAll(port1Path, 0755)
	assert.NoError(t, err)

	port2Path := filepath.Join(portsPath, "2")
	err = os.MkdirAll(port2Path, 0755)
	assert.NoError(t, err)

	t.Run("port dirs not exist", func(t *testing.T) {
		assert.False(t, IsRDMADeviceHealthy("unknown"))
	})

	t.Run("all ports active", func(t *testing.T) {
		err := os.WriteFile(filepath.Join(port1Path, "state"), []byte("4: ACTIVE"), 0644)
		assert.NoError(t, err)
		err = os.WriteFile(filepath.Join(port2Path, "state"), []byte("4: ACTIVE"), 0644)
		assert.NoError(t, err)

		assert.True(t, IsRDMADeviceHealthy(rdmaResource))
	})

	t.Run("one port not active", func(t *testing.T) {
		err := os.WriteFile(filepath.Join(port1Path, "state"), []byte("4: ACTIVE"), 0644)
		assert.NoError(t, err)
		err = os.WriteFile(filepath.Join(port2Path, "state"), []byte("1: DOWN"), 0644)
		assert.NoError(t, err)

		assert.False(t, IsRDMADeviceHealthy(rdmaResource))
	})

	t.Run("missing state file", func(t *testing.T) {
		err := os.Remove(filepath.Join(port2Path, "state"))
		assert.NoError(t, err)

		assert.False(t, IsRDMADeviceHealthy(rdmaResource))
	})
}
