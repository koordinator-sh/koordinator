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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criServer) Version(ctx context.Context, req *runtimeapi.VersionRequest) (*runtimeapi.VersionResponse, error) {
	return c.backendRuntimeServiceClient.Version(ctx, req)
}

func (c *criServer) RunPodSandbox(ctx context.Context, req *runtimeapi.RunPodSandboxRequest) (*runtimeapi.RunPodSandboxResponse, error) {
	rsp, err := c.InterceptRuntimeRequest(RunPodSandbox, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return c.backendRuntimeServiceClient.RunPodSandbox(ctx, req.(*runtimeapi.RunPodSandboxRequest))
		}, false)
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.RunPodSandboxResponse), err
}
func (c *criServer) StopPodSandbox(ctx context.Context, req *runtimeapi.StopPodSandboxRequest) (*runtimeapi.StopPodSandboxResponse, error) {
	rsp, err := c.InterceptRuntimeRequest(StopPodSandbox, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return c.backendRuntimeServiceClient.StopPodSandbox(ctx, req.(*runtimeapi.StopPodSandboxRequest))
		}, false)

	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.StopPodSandboxResponse), err
}

func (c *criServer) RemovePodSandbox(ctx context.Context, req *runtimeapi.RemovePodSandboxRequest) (*runtimeapi.RemovePodSandboxResponse, error) {
	return c.backendRuntimeServiceClient.RemovePodSandbox(ctx, req)
}

func (c *criServer) PodSandboxStatus(ctx context.Context, req *runtimeapi.PodSandboxStatusRequest) (*runtimeapi.PodSandboxStatusResponse, error) {
	return c.backendRuntimeServiceClient.PodSandboxStatus(ctx, req)
}

func (c *criServer) ListPodSandbox(ctx context.Context, req *runtimeapi.ListPodSandboxRequest) (*runtimeapi.ListPodSandboxResponse, error) {
	return c.backendRuntimeServiceClient.ListPodSandbox(ctx, req)
}

func (c *criServer) ListPodSandboxStats(ctx context.Context, req *runtimeapi.ListPodSandboxStatsRequest) (*runtimeapi.ListPodSandboxStatsResponse, error) {
	return c.backendRuntimeServiceClient.ListPodSandboxStats(ctx, req)
}

func (c *criServer) CreateContainer(ctx context.Context, req *runtimeapi.CreateContainerRequest) (*runtimeapi.CreateContainerResponse, error) {
	rsp, err := c.InterceptRuntimeRequest(CreateContainer, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return c.backendRuntimeServiceClient.CreateContainer(ctx, req.(*runtimeapi.CreateContainerRequest))
		}, false)
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.CreateContainerResponse), err
}

func (c *criServer) StartContainer(ctx context.Context, req *runtimeapi.StartContainerRequest) (*runtimeapi.StartContainerResponse, error) {
	rsp, err := c.InterceptRuntimeRequest(StartContainer, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return c.backendRuntimeServiceClient.StartContainer(ctx, req.(*runtimeapi.StartContainerRequest))
		}, false)
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.StartContainerResponse), err
}

func (c *criServer) StopContainer(ctx context.Context, req *runtimeapi.StopContainerRequest) (*runtimeapi.StopContainerResponse, error) {
	rsp, err := c.InterceptRuntimeRequest(StopContainer, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return c.backendRuntimeServiceClient.StopContainer(ctx, req.(*runtimeapi.StopContainerRequest))
		}, false)
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.StopContainerResponse), err
}

func (c *criServer) RemoveContainer(ctx context.Context, req *runtimeapi.RemoveContainerRequest) (*runtimeapi.RemoveContainerResponse, error) {
	rsp, err := c.InterceptRuntimeRequest(RemoveContainer, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return c.backendRuntimeServiceClient.RemoveContainer(ctx, req.(*runtimeapi.RemoveContainerRequest))
		}, false)
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.RemoveContainerResponse), err
}

func (c *criServer) ContainerStatus(ctx context.Context, req *runtimeapi.ContainerStatusRequest) (*runtimeapi.ContainerStatusResponse, error) {
	return c.backendRuntimeServiceClient.ContainerStatus(ctx, req)
}

func (c *criServer) ListContainers(ctx context.Context, req *runtimeapi.ListContainersRequest) (*runtimeapi.ListContainersResponse, error) {
	return c.backendRuntimeServiceClient.ListContainers(ctx, req)
}

func (c *criServer) UpdateContainerResources(ctx context.Context, req *runtimeapi.UpdateContainerResourcesRequest) (*runtimeapi.UpdateContainerResourcesResponse, error) {
	rsp, err := c.InterceptRuntimeRequest(UpdateContainerResources, ctx, req,
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return c.backendRuntimeServiceClient.UpdateContainerResources(ctx, req.(*runtimeapi.UpdateContainerResourcesRequest))
		}, false)
	if err != nil {
		return nil, err
	}
	return rsp.(*runtimeapi.UpdateContainerResourcesResponse), err
}

func (c *criServer) ContainerStats(ctx context.Context, req *runtimeapi.ContainerStatsRequest) (*runtimeapi.ContainerStatsResponse, error) {
	return c.backendRuntimeServiceClient.ContainerStats(ctx, req)
}
func (c *criServer) ListContainerStats(ctx context.Context, req *runtimeapi.ListContainerStatsRequest) (*runtimeapi.ListContainerStatsResponse, error) {
	return c.backendRuntimeServiceClient.ListContainerStats(ctx, req)
}

func (c *criServer) Status(ctx context.Context, req *runtimeapi.StatusRequest) (*runtimeapi.StatusResponse, error) {
	return c.backendRuntimeServiceClient.Status(ctx, req)
}

func (c *criServer) ReopenContainerLog(ctx context.Context, in *runtimeapi.ReopenContainerLogRequest) (*runtimeapi.ReopenContainerLogResponse, error) {
	return c.backendRuntimeServiceClient.ReopenContainerLog(ctx, in)
}
func (c *criServer) ExecSync(ctx context.Context, in *runtimeapi.ExecSyncRequest) (*runtimeapi.ExecSyncResponse, error) {
	return c.backendRuntimeServiceClient.ExecSync(ctx, in)
}
func (c *criServer) Exec(ctx context.Context, in *runtimeapi.ExecRequest) (*runtimeapi.ExecResponse, error) {
	return c.backendRuntimeServiceClient.Exec(ctx, in)
}

func (c *criServer) Attach(ctx context.Context, in *runtimeapi.AttachRequest) (*runtimeapi.AttachResponse, error) {
	return c.backendRuntimeServiceClient.Attach(ctx, in)
}

func (c *criServer) PortForward(ctx context.Context, in *runtimeapi.PortForwardRequest) (*runtimeapi.PortForwardResponse, error) {
	return c.backendRuntimeServiceClient.PortForward(ctx, in)
}

func (c *criServer) UpdateRuntimeConfig(ctx context.Context, in *runtimeapi.UpdateRuntimeConfigRequest) (*runtimeapi.UpdateRuntimeConfigResponse, error) {
	return c.backendRuntimeServiceClient.UpdateRuntimeConfig(ctx, in)
}

func (c *criServer) PodSandboxStats(ctx context.Context, in *runtimeapi.PodSandboxStatsRequest) (*runtimeapi.PodSandboxStatsResponse, error) {
	return c.backendRuntimeServiceClient.PodSandboxStats(ctx, in)
}

func (c *criServer) CheckpointContainer(ctx context.Context, req *runtimeapi.CheckpointContainerRequest) (*runtimeapi.CheckpointContainerResponse, error) {
	return c.backendRuntimeServiceClient.CheckpointContainer(ctx, req)
}

func (c *criServer) GetContainerEvents(req *runtimeapi.GetEventsRequest, server runtimeapi.RuntimeService_GetContainerEventsServer) error {
	return status.Errorf(codes.Unimplemented, "method GetContainerEvents not implemented")
}

func (c *criServer) ListMetricDescriptors(ctx context.Context, req *runtimeapi.ListMetricDescriptorsRequest) (*runtimeapi.ListMetricDescriptorsResponse, error) {
	return c.backendRuntimeServiceClient.ListMetricDescriptors(ctx, req)
}

func (c *criServer) ListPodSandboxMetrics(ctx context.Context, req *runtimeapi.ListPodSandboxMetricsRequest) (*runtimeapi.ListPodSandboxMetricsResponse, error) {
	return c.backendRuntimeServiceClient.ListPodSandboxMetrics(ctx, req)
}

func (c *criServer) RuntimeConfig(ctx context.Context, req *runtimeapi.RuntimeConfigRequest) (*runtimeapi.RuntimeConfigResponse, error) {
	return c.backendRuntimeServiceClient.RuntimeConfig(ctx, req)
}
