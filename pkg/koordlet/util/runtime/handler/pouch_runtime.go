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

	v1 "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	PouchEndpointSubFilepath = "pouchcri.sock"
)

func GetPouchEndpoint() string {
	return filepath.Join(system.Conf.VarRunRootDir, PouchEndpointSubFilepath)
}

type PouchRuntimeHandler struct {
	runtimeServiceClient v1.RuntimeServiceClient
	timeout              time.Duration
	endpoint             string
}

func NewPouchRuntimeHandler(endpoint string) (ContainerRuntimeHandler, error) {
	ep := strings.TrimPrefix(endpoint, "unix://")
	if _, err := os.Stat(ep); err != nil {
		return nil, err
	}
	// use v1alpha2 protocol
	client, err := getRuntimeV1alpha2Client(endpoint)
	if err != nil {
		return nil, err
	}

	return &PouchRuntimeHandler{
		runtimeServiceClient: client,
		timeout:              defaultConnectionTimeout,
		endpoint:             endpoint,
	}, nil
}

func (c *PouchRuntimeHandler) StopContainer(ctx context.Context, containerID string, timeout int64) error {
	if containerID == "" {
		return fmt.Errorf("containerID cannot be empty")
	}

	request := &v1.StopContainerRequest{
		ContainerId: containerID,
		Timeout:     timeout,
	}
	// pouch cannot handle context with timeout
	_, err := c.runtimeServiceClient.StopContainer(context.Background(), request)
	return err
}

func (c *PouchRuntimeHandler) UpdateContainerResources(containerID string, opts UpdateOptions) error {
	if containerID == "" {
		return fmt.Errorf("containerID cannot be empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	request := &v1.UpdateContainerResourcesRequest{
		ContainerId: containerID,
		Linux: &v1.LinuxContainerResources{
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

func getRuntimeV1alpha2Client(endpoint string) (v1.RuntimeServiceClient, error) {
	conn, err := getClientConnection(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %v", err)
	}
	runtimeClient := v1.NewRuntimeServiceClient(conn)
	return runtimeClient, nil
}
