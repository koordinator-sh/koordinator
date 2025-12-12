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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetGPUDevicePCIBusIDs(t *testing.T) {
	helper := NewFileTestUtil(t)
	defer helper.Cleanup()

	type args struct {
		dir   string
		files []string
	}

	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "case1: dir exist",
			args: args{
				dir: filepath.Join(helper.TempDir, "/sys/bus/pci/drivers/nvidia/"),
				files: []string{
					"0000:3b:00.1",
					"0000:01:00.0",
					"0000:af:ff.7",
					"bind",
					"new_id",
					"unbind",
					"uevent",
				},
			},
			want: []string{"0000:01:00.0", "0000:3b:00.1", "0000:af:ff.7"},
		},
		{
			name: "case2: dir not exist",
			args: args{
				dir:   filepath.Join(helper.TempDir, "/path/not/exist"),
				files: []string{},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			NVIDIADriverDir = tt.args.dir
			for _, driverFile := range tt.args.files {
				helper.CreateFile(filepath.Join(NVIDIADriverDir, driverFile))
			}
			got := GetGPUDevicePCIBusIDs()
			assert.Equal(t, tt.want, got)
		})
	}
}
