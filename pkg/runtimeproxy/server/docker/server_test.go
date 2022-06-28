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
	"context"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/server/types"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/store"
)

var _ proxyDockerClient = &fakeDockerClient{}

type fakeDockerClient struct {
	Information    dockertypes.Info
	ContatinerJson map[string]dockertypes.ContainerJSON
	Containers     []dockertypes.Container
}

func (f *fakeDockerClient) Info(ctx context.Context) (dockertypes.Info, error) {
	return f.Information, nil
}

func (f *fakeDockerClient) ContainerList(ctx context.Context, options dockertypes.ContainerListOptions) ([]dockertypes.Container, error) {
	return f.Containers, nil
}

func (f *fakeDockerClient) ContainerInspect(ctx context.Context, containerID string) (dockertypes.ContainerJSON, error) {
	return f.ContatinerJson[containerID], nil
}

func Test_failover(t *testing.T) {
	fakeClient := &fakeDockerClient{
		Information: dockertypes.Info{
			CgroupDriver: "systemd",
		},
		ContatinerJson: map[string]dockertypes.ContainerJSON{
			"id1": {
				ContainerJSONBase: &dockertypes.ContainerJSONBase{
					ID:         "id1",
					HostConfig: &container.HostConfig{},
				},
			},
			"id2": {
				ContainerJSONBase: &dockertypes.ContainerJSONBase{
					ID:         "id2",
					HostConfig: &container.HostConfig{},
				},
			},
		},
		Containers: []dockertypes.Container{
			{
				ID: "id1",
				Labels: map[string]string{
					types.ContainerTypeLabelKey: types.ContainerTypeLabelSandbox,
				},
			},
			{
				ID: "id2",
				Labels: map[string]string{
					types.ContainerTypeLabelKey: types.ContainerTypeLabelContainer,
					types.SandboxIDLabelKey:     "id1",
				},
			},
		},
	}
	manager := NewRuntimeManagerDockerServer()
	err := manager.failOver(fakeClient)
	assert.Equal(t, nil, err)
	assert.NotEqual(t, nil, store.GetContainerInfo("id2"))
	assert.NotEqual(t, nil, store.GetPodSandboxInfo("id1"))
}
