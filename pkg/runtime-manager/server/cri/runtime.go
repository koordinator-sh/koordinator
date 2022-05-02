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

package cri

import (
	"context"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func (ci *RuntimeManagerCriServer) Version(ctx context.Context, req *runtimeapi.VersionRequest) (*runtimeapi.VersionResponse, error) {
	return ci.backendClient.Version(ctx, req)
}

func (ci *RuntimeManagerCriServer) RunPodSandbox(ctx context.Context, req *runtimeapi.RunPodSandboxRequest) (*runtimeapi.RunPodSandboxResponse, error) {
	rsp, err := ci.interceptRuntimeRequest(RunPodSandbox, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return ci.backendClient.RunPodSandbox(ctx, req.(*runtimeapi.RunPodSandboxRequest))
		})
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.RunPodSandboxResponse), err
}
func (ci *RuntimeManagerCriServer) StopPodSandbox(ctx context.Context, req *runtimeapi.StopPodSandboxRequest) (*runtimeapi.StopPodSandboxResponse, error) {

	rsp, err := ci.interceptRuntimeRequest(StopPodSandbox, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return ci.backendClient.StopPodSandbox(ctx, req.(*runtimeapi.StopPodSandboxRequest))
		})

	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.StopPodSandboxResponse), err
}

func (ci *RuntimeManagerCriServer) RemovePodSandbox(ctx context.Context, req *runtimeapi.RemovePodSandboxRequest) (*runtimeapi.RemovePodSandboxResponse, error) {
	return ci.backendClient.RemovePodSandbox(ctx, req)
}

func (ci *RuntimeManagerCriServer) PodSandboxStatus(ctx context.Context, req *runtimeapi.PodSandboxStatusRequest) (*runtimeapi.PodSandboxStatusResponse, error) {
	return ci.backendClient.PodSandboxStatus(ctx, req)
}

func (ci *RuntimeManagerCriServer) ListPodSandbox(ctx context.Context, req *runtimeapi.ListPodSandboxRequest) (*runtimeapi.ListPodSandboxResponse, error) {
	return ci.backendClient.ListPodSandbox(ctx, req)
}

func (ci *RuntimeManagerCriServer) CreateContainer(ctx context.Context, req *runtimeapi.CreateContainerRequest) (*runtimeapi.CreateContainerResponse, error) {
	rsp, err := ci.interceptRuntimeRequest(CreateContainer, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return ci.backendClient.CreateContainer(ctx, req.(*runtimeapi.CreateContainerRequest))
		})
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.CreateContainerResponse), err
}

func (ci *RuntimeManagerCriServer) StartContainer(ctx context.Context, req *runtimeapi.StartContainerRequest) (*runtimeapi.StartContainerResponse, error) {
	rsp, err := ci.interceptRuntimeRequest(StartContainer, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return ci.backendClient.StartContainer(ctx, req.(*runtimeapi.StartContainerRequest))
		})
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.StartContainerResponse), err
}

func (ci *RuntimeManagerCriServer) StopContainer(ctx context.Context, req *runtimeapi.StopContainerRequest) (*runtimeapi.StopContainerResponse, error) {
	rsp, err := ci.interceptRuntimeRequest(StopContainer, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return ci.backendClient.StopContainer(ctx, req.(*runtimeapi.StopContainerRequest))
		})
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.StopContainerResponse), err
}

func (ci *RuntimeManagerCriServer) RemoveContainer(ctx context.Context, req *runtimeapi.RemoveContainerRequest) (*runtimeapi.RemoveContainerResponse, error) {
	rsp, err := ci.interceptRuntimeRequest(RemoveContainer, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return ci.backendClient.RemoveContainer(ctx, req.(*runtimeapi.RemoveContainerRequest))
		})
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.RemoveContainerResponse), err
}

func (ci *RuntimeManagerCriServer) ContainerStatus(ctx context.Context, req *runtimeapi.ContainerStatusRequest) (*runtimeapi.ContainerStatusResponse, error) {
	return ci.backendClient.ContainerStatus(ctx, req)
}

func (ci *RuntimeManagerCriServer) ListContainers(ctx context.Context, req *runtimeapi.ListContainersRequest) (*runtimeapi.ListContainersResponse, error) {
	return ci.backendClient.ListContainers(ctx, req)
}

func (ci *RuntimeManagerCriServer) UpdateContainerResources(ctx context.Context, req *runtimeapi.UpdateContainerResourcesRequest) (*runtimeapi.UpdateContainerResourcesResponse, error) {
	return ci.backendClient.UpdateContainerResources(ctx, req)
}

func (ci *RuntimeManagerCriServer) ContainerStats(ctx context.Context, req *runtimeapi.ContainerStatsRequest) (*runtimeapi.ContainerStatsResponse, error) {
	return ci.backendClient.ContainerStats(ctx, req)
}
func (ci *RuntimeManagerCriServer) ListContainerStats(ctx context.Context, req *runtimeapi.ListContainerStatsRequest) (*runtimeapi.ListContainerStatsResponse, error) {
	return ci.backendClient.ListContainerStats(ctx, req)
}

func (ci *RuntimeManagerCriServer) Status(ctx context.Context, req *runtimeapi.StatusRequest) (*runtimeapi.StatusResponse, error) {
	return ci.backendClient.Status(ctx, req)
}

func (ci *RuntimeManagerCriServer) ReopenContainerLog(ctx context.Context, in *runtimeapi.ReopenContainerLogRequest) (*runtimeapi.ReopenContainerLogResponse, error) {
	return ci.backendClient.ReopenContainerLog(ctx, in)
}
func (ci *RuntimeManagerCriServer) ExecSync(ctx context.Context, in *runtimeapi.ExecSyncRequest) (*runtimeapi.ExecSyncResponse, error) {
	return ci.backendClient.ExecSync(ctx, in)
}
func (ci *RuntimeManagerCriServer) Exec(ctx context.Context, in *runtimeapi.ExecRequest) (*runtimeapi.ExecResponse, error) {
	return ci.backendClient.Exec(ctx, in)
}

func (ci *RuntimeManagerCriServer) Attach(ctx context.Context, in *runtimeapi.AttachRequest) (*runtimeapi.AttachResponse, error) {
	return ci.backendClient.Attach(ctx, in)
}

func (ci *RuntimeManagerCriServer) PortForward(ctx context.Context, in *runtimeapi.PortForwardRequest) (*runtimeapi.PortForwardResponse, error) {
	return ci.backendClient.PortForward(ctx, in)
}

func (ci *RuntimeManagerCriServer) UpdateRuntimeConfig(ctx context.Context, in *runtimeapi.UpdateRuntimeConfigRequest) (*runtimeapi.UpdateRuntimeConfigResponse, error) {
	return ci.backendClient.UpdateRuntimeConfig(ctx, in)
}
