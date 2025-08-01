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

package xpu

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

func Test_readDeviceInfosFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "huawei-Ascend-910B.json")
	assert.NoError(t, err)

	xpuDevices := util.XPUDevices{
		{
			Vendor: "huawei",
			Model:  "Ascend-910B",
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102003",
			Minor:  "0",
			Resources: map[string]string{
				"huawei.com/npu-core":       "32",
				"hauwei.com/npu-cpu":        "14",
				"koordinator.sh/gpu-memory": "32Gi",
				"huawei.com/dvpp":           "100",
			},
			Topology: &util.DeviceTopology{
				P2PLinks: []util.DeviceP2PLink{
					{
						PeerMinor: "1,2,3",
						Type:      "HCCS",
					},
				},
				SocketID: "0",
				NodeID:   "0",
				PCIEID:   "0000:00:08.0",
				BusID:    "0000:00:08.0",
			},
			Status: &util.DeviceStatus{
				Healthy: true,
			},
		},
	}

	err = json.NewEncoder(tmpFile).Encode(xpuDevices)
	assert.NoError(t, err)
	tmpFile.Close()

	actual, err := readDeviceInfosFromFile(tmpFile.Name())
	assert.NoError(t, err)
	assert.Equal(t, xpuDevices, actual)
}

func Test_GetXPUDevice(t *testing.T) {
	tmpDir := t.TempDir()

	xpuDevices := util.XPUDevices{
		{
			Vendor: "huawei",
			Model:  "Ascend-910B",
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102003",
			Minor:  "0",
			Resources: map[string]string{
				"huawei.com/npu-core":       "32",
				"hauwei.com/npu-cpu":        "14",
				"koordinator.sh/gpu-memory": "32Gi",
				"huawei.com/dvpp":           "100",
			},
			Topology: &util.DeviceTopology{
				P2PLinks: []util.DeviceP2PLink{
					{
						PeerMinor: "1,2,3",
						Type:      "HCCS",
					},
				},
				SocketID: "0",
				NodeID:   "0",
				PCIEID:   "0000:00:08.0",
				BusID:    "0000:00:08.0",
			},
			Status: &util.DeviceStatus{
				Healthy: true,
			},
		},
	}

	tmpFile, err := os.CreateTemp(tmpDir, "huawei-Ascend-910B.json")
	assert.NoError(t, err)
	err = json.NewEncoder(tmpFile).Encode(xpuDevices)
	assert.NoError(t, err)
	tmpFile.Close()

	got, err := GetXPUDevice(tmpDir)
	assert.NoError(t, err)
	assert.Equal(t, xpuDevices, got)
}
