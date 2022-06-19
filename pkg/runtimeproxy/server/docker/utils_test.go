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

package docker

import (
	"bytes"
	"io"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	resource_executor "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/resexecutor"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/server/types"
)

func Test_calculateContentLength(t *testing.T) {
	type testCase struct {
		data      io.Reader
		expectLen int64
		expectErr bool
	}
	tests := []testCase{
		{
			bytes.NewBuffer([]byte("")),
			0,
			false,
		},
		{
			bytes.NewBuffer([]byte("1234567")),
			7,
			false,
		},
		{
			nil,
			-1,
			true,
		},
	}
	for _, tt := range tests {
		l, err := calculateContentLength(tt.data)
		assert.Equal(t, tt.expectErr, err != nil, err)
		assert.Equal(t, tt.expectLen, l)
	}
}

func Test_getContainerID(t *testing.T) {
	type testCase struct {
		url               string
		expectErr         bool
		expectContainerID string
	}
	tests := []testCase{
		{
			"/1.3/containers/5345hjkhjkf/start",
			false,
			"5345hjkhjkf",
		},
		{
			"1.3",
			true,
			"",
		},
	}
	for _, tt := range tests {
		cid, err := getContainerID(tt.url)
		assert.Equal(t, tt.expectErr, err != nil, err)
		assert.Equal(t, tt.expectContainerID, cid)
	}
}

func Test_splitLabelsAndAnnotations(t *testing.T) {
	type args struct {
		configs map[string]string
	}
	tests := []struct {
		name       string
		args       args
		wantLabels map[string]string
		wantAnnos  map[string]string
	}{
		{
			name: "Docker - normal case",
			args: args{
				configs: map[string]string{
					"annotation.dummy.koordinator.sh/test_splitLabelsAndAnnotations": "true",
					"io.kubernetes.docker.type":                                      "podsandbox",
				},
			},
			wantLabels: map[string]string{
				"io.kubernetes.docker.type": "podsandbox",
			},
			wantAnnos: map[string]string{
				"dummy.koordinator.sh/test_splitLabelsAndAnnotations": "true",
			},
		},
	}
	for _, tt := range tests {
		gotLabels, gotAnnos := splitLabelsAndAnnotations(tt.args.configs)
		assert.Equal(t, tt.wantLabels, gotLabels)
		assert.Equal(t, tt.wantAnnos, gotAnnos)
	}
}

func Test_toCriCgroupPath(t *testing.T) {
	type testCase struct {
		cgroupDriver     string
		cgroupParent     string
		expectCgroupPath string
	}

	tests := []testCase{
		{
			"systemd",
			"burstable/djfklsjdf98",
			"/kubepods.slice/kubepods-burstable.slice/burstable/djfklsjdf98",
		},
		{
			"cgroupfs",
			"/kubepods.slice/kubepods-burstable.slice/burstable/djfklsjdf98",
			"/kubepods.slice/kubepods-burstable.slice/burstable/djfklsjdf98",
		},
		{
			"systemd",
			"besteffort/fsdfsdf",
			"/kubepods.slice/kubepods-besteffort.slice/besteffort/fsdfsdf",
		},
		{
			"systemd",
			"dfsdfsdf",
			"/kubepods.slice/dfsdfsdf",
		},
	}
	for _, tt := range tests {
		path := ToCriCgroupPath(tt.cgroupDriver, tt.cgroupParent)
		assert.Equal(t, tt.expectCgroupPath, path)
	}
}

func Test_GetRuntimeResourceType(t *testing.T) {
	type testCase struct {
		labels       map[string]string
		expectedType resource_executor.RuntimeResourceType
	}
	tests := []testCase{
		{
			map[string]string{types.ContainerTypeLabelKey: types.ContainerTypeLabelSandbox},
			resource_executor.RuntimePodResource,
		},
		{
			map[string]string{types.ContainerTypeLabelKey: types.ContainerTypeLabelContainer},
			resource_executor.RuntimeContainerResource,
		},
	}
	for _, tt := range tests {
		resourceType := GetRuntimeResourceType(tt.labels)
		assert.Equal(t, tt.expectedType, resourceType)
	}
}

func Test_UpdateHostConfigByResource(t *testing.T) {
	type testCase struct {
		resources      *v1alpha1.LinuxContainerResources
		config         *container.HostConfig
		expectedConfig *container.HostConfig
	}
	tests := []testCase{
		{
			nil,
			nil,
			nil,
		},
		{&v1alpha1.LinuxContainerResources{
			CpuPeriod:   1000,
			CpuShares:   1000,
			OomScoreAdj: 10,
		},
			&container.HostConfig{
				Resources: container.Resources{
					CPUPeriod: 10,
					CPUShares: 20,
				},
				OomScoreAdj: 20,
			},
			&container.HostConfig{
				Resources: container.Resources{
					CPUPeriod: 1000,
					CPUShares: 1000,
				},
				OomScoreAdj: 10,
			},
		},
	}
	for _, tt := range tests {
		config := UpdateHostConfigByResource(tt.config, tt.resources)
		assert.Equal(t, tt.expectedConfig, config)
	}
}

func Test_UpdateUpdateConfigByResource(t *testing.T) {
	type testCase struct {
		resources      *v1alpha1.LinuxContainerResources
		config         *container.UpdateConfig
		expectedConfig *container.UpdateConfig
	}

	tests := []testCase{
		{
			nil,
			nil,
			nil,
		},
		{&v1alpha1.LinuxContainerResources{
			CpuPeriod:   1000,
			CpuShares:   1000,
			OomScoreAdj: 10,
		},
			&container.UpdateConfig{
				Resources: container.Resources{
					CPUPeriod: 10,
					CPUShares: 20,
				},
			},
			&container.UpdateConfig{
				Resources: container.Resources{
					CPUPeriod: 1000,
					CPUShares: 1000,
				},
			},
		},
	}
	for _, tt := range tests {
		config := UpdateUpdateConfigByResource(tt.config, tt.resources)
		assert.Equal(t, tt.expectedConfig, config)
	}
}
