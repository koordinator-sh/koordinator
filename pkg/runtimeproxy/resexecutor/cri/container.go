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
	"encoding/json"
	"fmt"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/store"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/utils"
)

type ContainerResourceExecutor struct {
	store.ContainerInfo
}

func NewContainerResourceExecutor() *ContainerResourceExecutor {
	return &ContainerResourceExecutor{}
}

func (c *ContainerResourceExecutor) String() string {
	return fmt.Sprintf("pod(%v/%v)container(%v)",
		c.GetPodMeta().GetName(), c.GetPodMeta().GetUid(),
		c.GetContainerMata().GetName())
}

func (c *ContainerResourceExecutor) GetMetaInfo() string {
	return fmt.Sprintf("pod(%v/%v)container(%v)",
		c.GetPodMeta().GetName(), c.GetPodMeta().GetUid(),
		c.GetContainerMata().GetName())
}

func (c *ContainerResourceExecutor) GenerateHookRequest() interface{} {
	return c.GetContainerResourceHookRequest()
}

func (c *ContainerResourceExecutor) loadContainerInfoFromStore(containerID, stage string) error {
	containerCheckPoint := store.GetContainerInfo(containerID)
	if containerCheckPoint == nil {
		return fmt.Errorf("fail to load container(%v) from store during %v", containerID, stage)
	}
	c.ContainerInfo = *containerCheckPoint
	klog.Infof("load container(%v) successful during %v ", containerID, stage)
	return nil
}

func (c *ContainerResourceExecutor) ParseRequest(req interface{}) error {
	switch request := req.(type) {
	case *runtimeapi.CreateContainerRequest:
		// get the pod info from local store
		podID := request.GetPodSandboxId()
		podCheckPoint := store.GetPodSandboxInfo(podID)
		if podCheckPoint == nil {
			return fmt.Errorf("fail to get pod(%v) related to container", podID)
		}
		c.ContainerInfo = store.ContainerInfo{
			ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
				PodMeta:      podCheckPoint.PodMeta,
				PodResources: podCheckPoint.Resources,
				ContainerMata: &v1alpha1.ContainerMetadata{
					Name:    request.GetConfig().GetMetadata().GetName(),
					Attempt: request.GetConfig().GetMetadata().GetAttempt(),
				},
				ContainerAnnotations: request.GetConfig().GetAnnotations(),
				ContainerResources:   transferResource(request.GetConfig().GetLinux().GetResources()),
			},
		}
		klog.Infof("success parse container info %v during container create", c)
	case *runtimeapi.StartContainerRequest:
		return c.loadContainerInfoFromStore(request.GetContainerId(), "StartContainer")
	case *runtimeapi.UpdateContainerResourcesRequest:
		return c.loadContainerInfoFromStore(request.GetContainerId(), "UpdateContainerResource")
	case *runtimeapi.StopContainerRequest:
		return c.loadContainerInfoFromStore(request.GetContainerId(), "StopContainer")
	}
	return nil
}

func (c *ContainerResourceExecutor) ParseContainer(container *runtimeapi.Container) error {
	if container == nil {
		return nil
	}
	podInfo := store.GetPodSandboxInfo(container.GetPodSandboxId())
	if podInfo == nil {
		return fmt.Errorf("fail to get pod info for %v", container.GetPodSandboxId())
	}
	c.ContainerInfo = store.ContainerInfo{
		ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
			ContainerAnnotations: container.GetAnnotations(),
			ContainerMata: &v1alpha1.ContainerMetadata{
				Name:    container.GetMetadata().GetName(),
				Attempt: container.GetMetadata().GetAttempt(),
			},
			PodMeta:        podInfo.GetPodMeta(),
			PodAnnotations: podInfo.GetAnnotations(),
			PodLabels:      podInfo.GetLabels(),
			// TODO: How to get resource when failOver
		},
	}
	return nil
}

func (c *ContainerResourceExecutor) ResourceCheckPoint(rsp interface{}) error {
	// container level resource checkpoint would be triggered during post container create only
	switch response := rsp.(type) {
	case *runtimeapi.CreateContainerResponse:
		err := store.WriteContainerInfo(response.GetContainerId(), &c.ContainerInfo)
		if err != nil {
			return err
		}
		data, _ := json.Marshal(c.ContainerInfo)
		klog.Infof("success to checkpoint container level info %v %v",
			response.GetContainerId(), string(data))
		return nil
	}
	return nil
}

func (c *ContainerResourceExecutor) DeleteCheckpointIfNeed(req interface{}) error {
	switch request := req.(type) {
	case *runtimeapi.StopContainerRequest:
		store.DeleteContainerInfo(request.GetContainerId())
	}
	return nil
}

func (c *ContainerResourceExecutor) UpdateResource(rsp interface{}) error {
	switch response := rsp.(type) {
	case *v1alpha1.ContainerResourceHookResponse:
		c.ContainerAnnotations = utils.MergeMap(c.ContainerAnnotations, response.ContainerAnnotations)
		c.ContainerResources = updateResource(c.ContainerResources, response.ContainerResources)
	}
	return nil
}
