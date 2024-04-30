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
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ValidateCgroupDriverType(t *testing.T) {
	t.Run("invalid cgroup driver type", func(t *testing.T) {
		assert.Equal(t, CgroupDriverType("").Validate(), false)
	})
	t.Run("valid cgroup driver type: systemd", func(t *testing.T) {
		assert.Equal(t, CgroupDriverType("systemd").Validate(), true)
	})
	t.Run("valid cgroup driver type: cgroupfs", func(t *testing.T) {
		assert.Equal(t, CgroupDriverType("cgroupfs").Validate(), true)
	})
}

func Test_ParsePodIDSystemd(t *testing.T) {
	testCases := []struct {
		basename  string
		wantError bool
		expeceted string
	}{
		{
			basename:  "kubepods-besteffort-pod12345.slice",
			expeceted: "12345",
		},
		{
			basename:  "kubepods-burstable-pod12345.slice",
			expeceted: "12345",
		},
		{
			basename:  "kubepods-pod12345.slice",
			expeceted: "12345",
		},
		{
			basename:  "pod12345",
			wantError: true,
		},
	}

	for _, tc := range testCases {
		actual, err := cgroupPathFormatterInSystemd.PodIDParser(tc.basename)
		if tc.wantError {
			assert.Error(t, err)
		} else {
			assert.Equal(t, tc.expeceted, actual)
		}
	}
}

func Test_ParsePodIDCgroupfs(t *testing.T) {
	testCases := []struct {
		basename  string
		wantError bool
		expeceted string
	}{
		{
			basename:  "pod12345",
			expeceted: "12345",
		},
		{
			basename:  "kubepods-pod12345.slice",
			wantError: true,
		},
	}

	for _, tc := range testCases {
		actual, err := cgroupPathFormatterInCgroupfs.PodIDParser(tc.basename)
		if tc.wantError {
			assert.Error(t, err)
		} else {
			assert.Equal(t, tc.expeceted, actual)
		}
	}
}

func Test_ParseContainerIDSystemd(t *testing.T) {
	testCases := []struct {
		basename  string
		wantError bool
		expeceted string
	}{
		{
			basename:  "docker-12345.scope",
			expeceted: "12345",
		},
		{
			basename:  "cri-containerd-12345.scope",
			expeceted: "12345",
		},
		{
			basename:  "crio-12345.scope",
			expeceted: "12345",
		},
		{
			basename:  "12345",
			wantError: true,
		},
	}

	for _, tc := range testCases {
		actual, err := cgroupPathFormatterInSystemd.ContainerIDParser(tc.basename)
		if tc.wantError {
			assert.Error(t, err)
		} else {
			assert.Equal(t, tc.expeceted, actual)
		}
	}
}

func Test_ParseContainerIDCgroupfs(t *testing.T) {
	testCases := []struct {
		basename  string
		wantError bool
		expeceted string
	}{
		{
			basename:  "12345",
			expeceted: "12345",
		},
		{
			basename:  "docker-12345.scope",
			expeceted: "docker-12345.scope",
		},
	}

	for _, tc := range testCases {
		actual, err := cgroupPathFormatterInCgroupfs.ContainerIDParser(tc.basename)
		if tc.wantError {
			assert.Error(t, err)
		} else {
			assert.Equal(t, tc.expeceted, actual)
		}
	}
}

func Test_SystemdCgroupPathContainerDirFn(t *testing.T) {
	testCases := []struct {
		name        string
		containerID string
		wantType    string
		wantDirName string
		wantError   bool
	}{
		{
			name:        "docker",
			containerID: "docker://testDockerContainerID",
			wantType:    RuntimeTypeDocker,
			wantDirName: "docker-testDockerContainerID.scope",
			wantError:   false,
		},
		{
			name:        "containerd",
			containerID: "containerd://testContainerdContainerID",
			wantType:    RuntimeTypeContainerd,
			wantDirName: "cri-containerd-testContainerdContainerID.scope",
			wantError:   false,
		},
		{
			name:        "pouch",
			containerID: "pouch://testPouchContainerID",
			wantType:    RuntimeTypePouch,
			wantDirName: "pouch-testPouchContainerID.scope",
			wantError:   false,
		},
		{
			name:        "cri-o",
			containerID: "cri-o://testCrioContainerID",
			wantType:    RuntimeTypeCrio,
			wantDirName: "crio-testCrioContainerID.scope",
			wantError:   false,
		},
		{
			name:        "bad-format",
			containerID: "bad-format-id",
			wantType:    RuntimeTypeUnknown,
			wantDirName: "",
			wantError:   true,
		},
	}
	for _, tc := range testCases {
		runtimeType, dirName, gotErr := cgroupPathFormatterInSystemd.ContainerDirFn(tc.containerID)
		assert.Equal(t, tc.wantType, runtimeType)
		assert.Equal(t, tc.wantDirName, dirName)
		assert.Equal(t, tc.wantError, gotErr != nil)
	}
}

func Test_CgroupfsCgroupPathContainerDirFn(t *testing.T) {
	testCases := []struct {
		name        string
		containerID string
		wantType    string
		wantDirName string
		wantError   bool
	}{
		{
			name:        "docker",
			containerID: "docker://testDockerContainerID",
			wantType:    RuntimeTypeDocker,
			wantDirName: "testDockerContainerID",
			wantError:   false,
		},
		{
			name:        "containerd",
			containerID: "containerd://testContainerdContainerID",
			wantType:    RuntimeTypeContainerd,
			wantDirName: "testContainerdContainerID",
			wantError:   false,
		},
		{
			name:        "pouch",
			containerID: "pouch://testPouchContainerID",
			wantType:    RuntimeTypePouch,
			wantDirName: "testPouchContainerID",
			wantError:   false,
		},
		{
			name:        "cri-o",
			containerID: "cri-o://testCrioContainerID",
			wantType:    RuntimeTypeCrio,
			wantDirName: "testCrioContainerID",
			wantError:   false,
		},
		{
			name:        "bad-format",
			containerID: "bad-format-id",
			wantType:    RuntimeTypeUnknown,
			wantDirName: "",
			wantError:   true,
		},
	}
	for _, tc := range testCases {
		runtimeType, dirName, gotErr := cgroupPathFormatterInCgroupfs.ContainerDirFn(tc.containerID)
		assert.Equal(t, tc.wantType, runtimeType)
		assert.Equal(t, tc.wantDirName, dirName)
		assert.Equal(t, tc.wantError, gotErr != nil)
	}
}
