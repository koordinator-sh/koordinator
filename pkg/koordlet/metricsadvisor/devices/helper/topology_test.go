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

package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_parseGPUPCIInfo(t *testing.T) {
	tests := []struct {
		name      string
		busID     string
		nodeID    int32
		pcie      string
		wantNode  int32
		wantPCIE  string
		wantBusID string
		wantErr   bool
	}{
		{
			name:      "numa node -1",
			busID:     "0000:00:07.0",
			nodeID:    -1,
			pcie:      "pci0000:00",
			wantNode:  0,
			wantPCIE:  "pci0000:00",
			wantBusID: "0000:00:07.0",
		},
		{
			name:      "numa node 1",
			busID:     "0000:00:07.0",
			nodeID:    1,
			pcie:      "pci0000:00",
			wantNode:  1,
			wantPCIE:  "pci0000:00",
			wantBusID: "0000:00:07.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()

			pciDeviceDir := system.GetPCIDeviceDir()
			gpuDeviceDir := filepath.Join(pciDeviceDir, tt.pcie, tt.busID)
			assert.NoError(t, os.MkdirAll(gpuDeviceDir, 0700))
			assert.NoError(t, os.WriteFile(filepath.Join(gpuDeviceDir, "numa_node"), []byte(fmt.Sprintf("%d\n", tt.nodeID)), 0700))

			symbolicLink := filepath.Join(pciDeviceDir, tt.busID)
			assert.NoError(t, os.Symlink(gpuDeviceDir, symbolicLink))

			var busIdLegacy [16]int8
			for i, v := range tt.busID {
				busIdLegacy[i] = int8(v)
			}
			nodeID, pcie, busID, err := ParsePCIInfo(tt.busID)
			if (err != nil) && !tt.wantErr {
				t.Errorf("expect wantErr=%v but got err=%v", tt.wantErr, err)
				return
			}
			assert.Equal(t, tt.wantNode, nodeID)
			assert.Equal(t, tt.wantPCIE, pcie)
			assert.Equal(t, tt.wantBusID, busID)
		})
	}
}
