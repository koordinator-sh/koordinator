/*
 * Copyright 2022 The Koordinator Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package handler

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/moby/moby/client"
)

type DockerRuntimeHandler struct {
	dockerClient *client.Client
	endpoint     string
}

func NewDockerRuntimeHandler(endpoint string) (ContainerRuntimeHandler, error) {
	dockerClient, err := createDockerClient(endpoint)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultConnectionTimeout)
	defer cancel()
	dockerClient.NegotiateAPIVersion(ctx)

	return &DockerRuntimeHandler{
		dockerClient: dockerClient,
		endpoint:     endpoint,
	}, err
}

func createDockerClient(endpoint string) (*client.Client, error) {
	ep := strings.TrimPrefix(endpoint, "unix://")
	if _, err := os.Stat(ep); err != nil {
		return nil, err
	}
	return client.NewClientWithOpts(client.WithHost(endpoint))
}

func (d *DockerRuntimeHandler) StopContainer(containerID string, timeout int64) error {
	if containerID == "" {
		return fmt.Errorf("container ID cannot be empty")
	}

	if d == nil || d.dockerClient == nil {
		return fmt.Errorf("failed to stop container %v, docker client is nil", containerID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultConnectionTimeout)
	defer cancel()

	stopTimeout := time.Duration(timeout) * time.Second
	return d.dockerClient.ContainerStop(ctx, containerID, &stopTimeout)
}

func (d *DockerRuntimeHandler) UpdateContainerResources(containerID string, opts UpdateOptions) error {
	if containerID == "" {
		return fmt.Errorf("container ID cannot be empty")
	}

	if d == nil || d.dockerClient == nil {
		return fmt.Errorf("failed to stop container %v, docker client is nil", containerID)
	}

	updateConfig := container.UpdateConfig{
		Resources: container.Resources{
			CPUPeriod:  opts.CPUPeriod,
			CPUQuota:   opts.CPUQuota,
			CPUShares:  opts.CPUShares,
			CpusetCpus: opts.CpusetCpus,
			CpusetMems: opts.CpusetMems,
			Memory:     opts.MemoryLimitInBytes,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultConnectionTimeout)
	defer cancel()
	_, err := d.dockerClient.ContainerUpdate(ctx, containerID, updateConfig)

	return err
}
