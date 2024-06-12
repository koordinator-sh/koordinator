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

package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func GetCrioEndpoint() string {
	return filepath.Join(system.Conf.VarRunRootDir, "crio/crio.sock")
}

func GetCrioEndpoint2() string {
	return filepath.Join(system.Conf.VarRunRootDir, "crio.sock")
}

type CrioRuntimeHandler struct {
	runtimeServiceClient runtimeapi.RuntimeServiceClient
	timeout              time.Duration
	endpoint             string
}

func NewCrioRuntimeHandler(endpoint string) (ContainerRuntimeHandler, error) {
	ep := strings.TrimPrefix(endpoint, "unix://")
	if _, err := os.Stat(ep); err != nil {
		return nil, err
	}

	client, err := getRuntimeClient(endpoint)
	if err != nil {
		return nil, err
	}

	return &CrioRuntimeHandler{
		runtimeServiceClient: client,
		timeout:              defaultConnectionTimeout,
		endpoint:             endpoint,
	}, nil
}

func (c *CrioRuntimeHandler) StopContainer(ctx context.Context, containerID string, timeout int64) error {
	if containerID == "" {
		return fmt.Errorf("containerID cannot be empty")
	}
	t := c.timeout + time.Duration(timeout)
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()

	request := &runtimeapi.StopContainerRequest{
		ContainerId: containerID,
		Timeout:     timeout,
	}
	_, err := c.runtimeServiceClient.StopContainer(ctx, request)
	return err
}

func (c *CrioRuntimeHandler) UpdateContainerResources(containerID string, opts UpdateOptions) error {
	if containerID == "" {
		return fmt.Errorf("containerID cannot be empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	request := &runtimeapi.UpdateContainerResourcesRequest{
		ContainerId: containerID,
		Linux: &runtimeapi.LinuxContainerResources{
			CpuPeriod:          opts.CPUPeriod,
			CpuQuota:           opts.CPUQuota,
			CpuShares:          opts.CPUShares,
			CpusetCpus:         opts.CpusetCpus,
			CpusetMems:         opts.CpusetMems,
			MemoryLimitInBytes: opts.MemoryLimitInBytes,
			OomScoreAdj:        opts.OomScoreAdj,
		},
	}
	_, err := c.runtimeServiceClient.UpdateContainerResources(ctx, request)
	return err
}
