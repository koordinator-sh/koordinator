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
	"errors"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	runtimeapialpha "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

type ServiceType int
type RuntimeServiceType int
type ImageServiceType int

const (
	RuntimeService ServiceType = iota
	ImageService
)

const (
	RunPodSandbox RuntimeServiceType = iota
	StopPodSandbox
	CreateContainer
	StartContainer
	StopContainer
	RemoveContainer
	UpdateContainerResources
)

func convert(
	input interface{ Marshal() ([]byte, error) },
	output interface{ Unmarshal(_ []byte) error },
) error {
	p, err := input.Marshal()
	if err != nil {
		return err
	}

	if err = output.Unmarshal(p); err != nil {
		return err
	}
	return nil
}

func alphaObjectToV1Object(obj interface{}) (interface{}, error) {
	var v1Object interface{ Unmarshal(_ []byte) error }
	switch obj.(type) {
	case *runtimeapialpha.VersionRequest:
		v1Object = &runtimeapi.VersionRequest{}
	case *runtimeapialpha.VersionResponse:
		v1Object = &runtimeapi.VersionResponse{}
	case *runtimeapialpha.RunPodSandboxRequest:
		v1Object = &runtimeapi.RunPodSandboxRequest{}
	case *runtimeapialpha.RunPodSandboxResponse:
		v1Object = &runtimeapi.RunPodSandboxResponse{}
	case *runtimeapialpha.StopPodSandboxRequest:
		v1Object = &runtimeapi.StopPodSandboxRequest{}
	case *runtimeapialpha.StopPodSandboxResponse:
		v1Object = &runtimeapi.StopPodSandboxResponse{}
	case *runtimeapialpha.RemovePodSandboxRequest:
		v1Object = &runtimeapi.RemovePodSandboxRequest{}
	case *runtimeapialpha.RemovePodSandboxResponse:
		v1Object = &runtimeapi.RemovePodSandboxResponse{}
	case *runtimeapialpha.PodSandboxStatusRequest:
		v1Object = &runtimeapi.PodSandboxStatusRequest{}
	case *runtimeapialpha.PodSandboxStatusResponse:
		v1Object = &runtimeapi.PodSandboxStatusResponse{}
	case *runtimeapialpha.ListPodSandboxRequest:
		v1Object = &runtimeapi.ListPodSandboxRequest{}
	case *runtimeapialpha.ListPodSandboxResponse:
		v1Object = &runtimeapi.ListPodSandboxResponse{}
	case *runtimeapialpha.CreateContainerRequest:
		v1Object = &runtimeapi.CreateContainerRequest{}
	case *runtimeapialpha.CreateContainerResponse:
		v1Object = &runtimeapi.CreateContainerResponse{}
	case *runtimeapialpha.StartContainerRequest:
		v1Object = &runtimeapi.StartContainerRequest{}
	case *runtimeapialpha.StartContainerResponse:
		v1Object = &runtimeapi.StartContainerResponse{}
	case *runtimeapialpha.StopContainerRequest:
		v1Object = &runtimeapi.StopContainerRequest{}
	case *runtimeapialpha.StopContainerResponse:
		v1Object = &runtimeapi.StopContainerResponse{}
	case *runtimeapialpha.RemoveContainerRequest:
		v1Object = &runtimeapi.RemoveContainerRequest{}
	case *runtimeapialpha.RemoveContainerResponse:
		v1Object = &runtimeapi.RemoveContainerResponse{}
	case *runtimeapialpha.ContainerStatusRequest:
		v1Object = &runtimeapi.ContainerStatusRequest{}
	case *runtimeapialpha.ContainerStatusResponse:
		v1Object = &runtimeapi.ContainerStatusResponse{}
	case *runtimeapialpha.ListContainersRequest:
		v1Object = &runtimeapi.ListContainersRequest{}
	case *runtimeapialpha.ListContainersResponse:
		v1Object = &runtimeapi.ListContainersResponse{}
	case *runtimeapialpha.UpdateContainerResourcesRequest:
		v1Object = &runtimeapi.UpdateContainerResourcesRequest{}
	case *runtimeapialpha.UpdateContainerResourcesResponse:
		v1Object = &runtimeapi.UpdateContainerResourcesResponse{}
	case *runtimeapialpha.ContainerStatsRequest:
		v1Object = &runtimeapi.ContainerStatsRequest{}
	case *runtimeapialpha.ContainerStatsResponse:
		v1Object = &runtimeapi.ContainerStatsResponse{}
	case *runtimeapialpha.ListContainerStatsRequest:
		v1Object = &runtimeapi.ListContainerStatsRequest{}
	case *runtimeapialpha.ListContainerStatsResponse:
		v1Object = &runtimeapi.ListContainerStatsResponse{}
	case *runtimeapialpha.StatusRequest:
		v1Object = &runtimeapi.StatusRequest{}
	case *runtimeapialpha.StatusResponse:
		v1Object = &runtimeapi.StatusResponse{}
	case *runtimeapialpha.ReopenContainerLogRequest:
		v1Object = &runtimeapi.ReopenContainerLogRequest{}
	case *runtimeapialpha.ReopenContainerLogResponse:
		v1Object = &runtimeapi.ReopenContainerLogResponse{}
	case *runtimeapialpha.ExecSyncRequest:
		v1Object = &runtimeapi.ExecSyncRequest{}
	case *runtimeapialpha.ExecSyncResponse:
		v1Object = &runtimeapi.ExecSyncResponse{}
	case *runtimeapialpha.ExecRequest:
		v1Object = &runtimeapi.ExecRequest{}
	case *runtimeapialpha.ExecResponse:
		v1Object = &runtimeapi.ExecResponse{}
	case *runtimeapialpha.AttachRequest:
		v1Object = &runtimeapi.AttachRequest{}
	case *runtimeapialpha.AttachResponse:
		v1Object = &runtimeapi.AttachResponse{}
	case *runtimeapialpha.PortForwardRequest:
		v1Object = &runtimeapi.PortForwardRequest{}
	case *runtimeapialpha.PortForwardResponse:
		v1Object = &runtimeapi.PortForwardResponse{}
	case *runtimeapialpha.UpdateRuntimeConfigRequest:
		v1Object = &runtimeapi.UpdateRuntimeConfigRequest{}
	case *runtimeapialpha.UpdateRuntimeConfigResponse:
		v1Object = &runtimeapi.UpdateRuntimeConfigResponse{}
	default:
		return nil, errors.New("invalid v1alpha2 cri api object")
	}
	err := convert(obj.(interface{ Marshal() ([]byte, error) }), v1Object)
	if err != nil {
		return nil, err
	}
	return v1Object, nil
}

func v1ObjectToAlphaObject(obj interface{}) (interface{}, error) {
	var alphaObject interface{ Unmarshal(_ []byte) error }
	switch obj.(type) {
	case *runtimeapi.VersionRequest:
		alphaObject = &runtimeapialpha.VersionRequest{}
	case *runtimeapi.VersionResponse:
		alphaObject = &runtimeapialpha.VersionResponse{}
	case *runtimeapi.RunPodSandboxRequest:
		alphaObject = &runtimeapialpha.RunPodSandboxRequest{}
	case *runtimeapi.RunPodSandboxResponse:
		alphaObject = &runtimeapialpha.RunPodSandboxResponse{}
	case *runtimeapi.StopPodSandboxRequest:
		alphaObject = &runtimeapialpha.StopPodSandboxRequest{}
	case *runtimeapi.StopPodSandboxResponse:
		alphaObject = &runtimeapialpha.StopPodSandboxResponse{}
	case *runtimeapi.RemovePodSandboxRequest:
		alphaObject = &runtimeapialpha.RemovePodSandboxRequest{}
	case *runtimeapi.RemovePodSandboxResponse:
		alphaObject = &runtimeapialpha.RemovePodSandboxResponse{}
	case *runtimeapi.PodSandboxStatusRequest:
		alphaObject = &runtimeapialpha.PodSandboxStatusRequest{}
	case *runtimeapi.PodSandboxStatusResponse:
		alphaObject = &runtimeapialpha.PodSandboxStatusResponse{}
	case *runtimeapi.ListPodSandboxRequest:
		alphaObject = &runtimeapialpha.ListPodSandboxRequest{}
	case *runtimeapi.ListPodSandboxResponse:
		alphaObject = &runtimeapialpha.ListPodSandboxResponse{}
	case *runtimeapi.CreateContainerRequest:
		alphaObject = &runtimeapialpha.CreateContainerRequest{}
	case *runtimeapi.CreateContainerResponse:
		alphaObject = &runtimeapialpha.CreateContainerResponse{}
	case *runtimeapi.StartContainerRequest:
		alphaObject = &runtimeapialpha.StartContainerRequest{}
	case *runtimeapi.StartContainerResponse:
		alphaObject = &runtimeapialpha.StartContainerResponse{}
	case *runtimeapi.StopContainerRequest:
		alphaObject = &runtimeapialpha.StopContainerRequest{}
	case *runtimeapi.StopContainerResponse:
		alphaObject = &runtimeapialpha.StopContainerResponse{}
	case *runtimeapi.RemoveContainerRequest:
		alphaObject = &runtimeapialpha.RemoveContainerRequest{}
	case *runtimeapi.RemoveContainerResponse:
		alphaObject = &runtimeapialpha.RemoveContainerResponse{}
	case *runtimeapi.ContainerStatusRequest:
		alphaObject = &runtimeapialpha.ContainerStatusRequest{}
	case *runtimeapi.ContainerStatusResponse:
		alphaObject = &runtimeapialpha.ContainerStatusResponse{}
	case *runtimeapi.ListContainersRequest:
		alphaObject = &runtimeapialpha.ListContainersRequest{}
	case *runtimeapi.ListContainersResponse:
		alphaObject = &runtimeapialpha.ListContainersResponse{}
	case *runtimeapi.UpdateContainerResourcesRequest:
		alphaObject = &runtimeapialpha.UpdateContainerResourcesRequest{}
	case *runtimeapi.UpdateContainerResourcesResponse:
		alphaObject = &runtimeapialpha.UpdateContainerResourcesResponse{}
	case *runtimeapi.ContainerStatsRequest:
		alphaObject = &runtimeapialpha.ContainerStatsRequest{}
	case *runtimeapi.ContainerStatsResponse:
		alphaObject = &runtimeapialpha.ContainerStatsResponse{}
	case *runtimeapi.ListContainerStatsRequest:
		alphaObject = &runtimeapialpha.ListContainerStatsRequest{}
	case *runtimeapi.ListContainerStatsResponse:
		alphaObject = &runtimeapialpha.ListContainerStatsResponse{}
	case *runtimeapi.StatusRequest:
		alphaObject = &runtimeapialpha.StatusRequest{}
	case *runtimeapi.StatusResponse:
		alphaObject = &runtimeapialpha.StatusResponse{}
	case *runtimeapi.ReopenContainerLogRequest:
		alphaObject = &runtimeapialpha.ReopenContainerLogRequest{}
	case *runtimeapi.ReopenContainerLogResponse:
		alphaObject = &runtimeapialpha.ReopenContainerLogResponse{}
	case *runtimeapi.ExecSyncRequest:
		alphaObject = &runtimeapialpha.ExecSyncRequest{}
	case *runtimeapi.ExecSyncResponse:
		alphaObject = &runtimeapialpha.ExecSyncResponse{}
	case *runtimeapi.ExecRequest:
		alphaObject = &runtimeapialpha.ExecRequest{}
	case *runtimeapi.ExecResponse:
		alphaObject = &runtimeapialpha.ExecResponse{}
	case *runtimeapi.AttachRequest:
		alphaObject = &runtimeapialpha.AttachRequest{}
	case *runtimeapi.AttachResponse:
		alphaObject = &runtimeapialpha.AttachResponse{}
	case *runtimeapi.PortForwardRequest:
		alphaObject = &runtimeapialpha.PortForwardRequest{}
	case *runtimeapi.PortForwardResponse:
		alphaObject = &runtimeapialpha.PortForwardResponse{}
	case *runtimeapi.UpdateRuntimeConfigRequest:
		alphaObject = &runtimeapialpha.UpdateRuntimeConfigRequest{}
	case *runtimeapi.UpdateRuntimeConfigResponse:
		alphaObject = &runtimeapialpha.UpdateRuntimeConfigResponse{}
	default:
		return nil, errors.New("invalid v1alpha2 cri api object")
	}
	err := convert(obj.(interface{ Marshal() ([]byte, error) }), alphaObject)
	if err != nil {
		return nil, err
	}
	return alphaObject, nil
}
