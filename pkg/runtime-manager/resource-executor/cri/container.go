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
	"github.com/koordinator-sh/koordinator/pkg/runtime-manager/store"
	"github.com/koordinator-sh/koordinator/pkg/runtime-manager/utils"
)

type ContainerResourceExecutor struct {
	store                *store.MetaManager
	PodMeta              *v1alpha1.PodSandboxMetadata
	ContainerMata        *v1alpha1.ContainerMetadata
	ContainerAnnotations map[string]string
	ContainerResources   *v1alpha1.LinuxContainerResources
	PodResources         *v1alpha1.LinuxContainerResources
}

func NewContainerResourceExecutor(store *store.MetaManager) *ContainerResourceExecutor {
	return &ContainerResourceExecutor{
		store: store,
	}
}

func (c *ContainerResourceExecutor) String() string {
	return fmt.Sprintf("pod(%v/%v)container(%v)",
		c.PodMeta.Name, c.PodMeta.Uid,
		c.ContainerMata.Name)
}

func (c *ContainerResourceExecutor) GetMetaInfo() string {
	return fmt.Sprintf("pod(%v/%v)container(%v)",
		c.PodMeta.Name, c.PodMeta.Uid,
		c.ContainerMata.Name)
}

func (c *ContainerResourceExecutor) GenerateResourceCheckpoint() interface{} {
	return &store.ContainerInfo{
		PodMeta:              c.PodMeta,
		ContainerMata:        c.ContainerMata,
		ContainerAnnotations: c.ContainerAnnotations,
		ContainerResources:   c.ContainerResources,
		PodResources:         c.PodResources,
	}
}

func (c *ContainerResourceExecutor) GenerateHookRequest() interface{} {
	return &v1alpha1.ContainerResourceHookRequest{
		PodMeta:              c.PodMeta,
		ContainerMata:        c.ContainerMata,
		ContainerAnnotations: c.ContainerAnnotations,
		ContainerResources:   c.ContainerResources,
		PodResources:         c.PodResources,
	}
}

func (c *ContainerResourceExecutor) updateByCheckPoint(containerID string) error {
	containerCheckPoint := c.store.GetContainerInfo(containerID)
	if containerCheckPoint != nil {
		return fmt.Errorf("fail to get ")
	}
	c.PodMeta = containerCheckPoint.PodMeta
	c.PodResources = containerCheckPoint.PodResources
	c.ContainerMata = containerCheckPoint.ContainerMata
	c.ContainerResources = containerCheckPoint.ContainerResources
	c.ContainerAnnotations = containerCheckPoint.ContainerAnnotations
	klog.Infof("get container info successful %v", containerID)
	return nil
}

func (c *ContainerResourceExecutor) ParseRequest(req interface{}) error {
	switch request := req.(type) {
	case *runtimeapi.CreateContainerRequest:
		// get the pod info from local store
		podID := request.GetPodSandboxId()
		podCheckPoint := c.store.GetPodSandboxInfo(podID)
		if podCheckPoint == nil {
			return fmt.Errorf("no pod info related to %v", podID)
		}
		c.PodMeta = podCheckPoint.PodMeta
		c.PodResources = podCheckPoint.Resources

		// construct container info
		c.ContainerMata = &v1alpha1.ContainerMetadata{
			Name:    request.GetConfig().GetMetadata().GetName(),
			Attempt: request.GetConfig().GetMetadata().GetAttempt(),
		}
		c.ContainerAnnotations = request.GetConfig().GetAnnotations()
		c.ContainerResources = transferResource(request.GetConfig().GetLinux().GetResources())
		klog.Infof("success parse container info %v during container create", c)
	case *runtimeapi.StartContainerRequest:
		if err := c.updateByCheckPoint(request.GetContainerId()); err != nil {
			return err
		}
		klog.Infof("success parse container Info %v during container start", c)
	case *runtimeapi.UpdateContainerResourcesRequest:
		if err := c.updateByCheckPoint(request.GetContainerId()); err != nil {
			return err
		}
		klog.Infof("success parse container Info %v during container update resource", c)
	case *runtimeapi.StopContainerRequest:
		if err := c.updateByCheckPoint(request.GetContainerId()); err != nil {
			return nil
		}
		klog.Infof("success parse container Info %v during container stop", c)
	}
	return nil
}

func (c *ContainerResourceExecutor) ParseContainer(container *runtimeapi.Container) error {
	if container == nil {
		return nil
	}
	podInfo := c.store.GetPodSandboxInfo(container.GetPodSandboxId())
	if podInfo == nil {
		return fmt.Errorf("fail to get pod info for %v", container.GetPodSandboxId())
	}

	c.ContainerAnnotations = container.GetAnnotations()
	c.ContainerMata = &v1alpha1.ContainerMetadata{
		Name:    container.GetMetadata().GetName(),
		Attempt: container.GetMetadata().GetAttempt(),
	}
	c.PodMeta = podInfo.PodMeta
	// TODO: How to get resource when failOver
	return nil
}

func (c *ContainerResourceExecutor) ResourceCheckPoint(rsp interface{}) error {
	// container level resource checkpoint would be triggered during post container create only
	switch response := rsp.(type) {
	case *runtimeapi.CreateContainerResponse:
		containerCheckpoint := c.GenerateResourceCheckpoint().(*store.ContainerInfo)
		data, _ := json.Marshal(containerCheckpoint)
		err := c.store.WriteContainerInfo(response.GetContainerId(), containerCheckpoint)
		if err != nil {
			return err
		}
		klog.Infof("success to checkpoint container level info %v %v",
			response.GetContainerId(), string(data))
		return nil
	}
	return nil
}

func (c *ContainerResourceExecutor) DeleteCheckpointIfNeed(req interface{}) error {
	switch request := req.(type) {
	case *runtimeapi.StopContainerRequest:
		c.store.DeleteContainerInfo(request.GetContainerId())
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
