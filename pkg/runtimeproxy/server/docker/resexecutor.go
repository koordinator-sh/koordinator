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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/docker/docker/api/types/container"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/server/types"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/store"
)

type PodResourceExecutorDocker struct {
	cgroupDriver string
	config.RuntimeRequestPath
	*types.ConfigWrapper
	*container.UpdateConfig

	*store.PodSandboxInfo
	*store.ContainerInfo

	id string
}

func (p *PodResourceExecutorDocker) GenerateHookRequest() interface{} {
	switch p.RuntimeRequestPath {
	case config.RunPodSandbox:
		fallthrough
	case config.StopPodSandbox:
		return p.GetPodSandboxHookRequest()
	case config.CreateContainer:
		fallthrough
	case config.StartContainer:
		fallthrough
	case config.UpdateContainerResources:
		fallthrough
	case config.StopContainer:
		return p.GetContainerResourceHookRequest()
	}
	return nil
}

func (p *PodResourceExecutorDocker) getMeta(req *http.Request) ([]string, error) {
	containerName := req.URL.Query().Get("name")
	tokens := strings.Split(containerName, "_")
	if len(tokens) != 6 {
		return nil, fmt.Errorf("failed to split k8s container name from %s", containerName)
	}
	return tokens, nil
}

func (p *PodResourceExecutorDocker) getPodMeta(req *http.Request) (*v1alpha1.PodSandboxMetadata, error) {
	tokens, err := p.getMeta(req)
	if err != nil {
		return nil, err
	}
	return &v1alpha1.PodSandboxMetadata{
		Name:      tokens[2],
		Namespace: tokens[3],
		Uid:       tokens[4],
	}, nil
}

func (p *PodResourceExecutorDocker) getContainerMeta(req *http.Request) (*v1alpha1.ContainerMetadata, error) {
	tokens, err := p.getMeta(req)
	if err != nil {
		return nil, err
	}
	return &v1alpha1.ContainerMetadata{
		Name: tokens[1],
	}, nil
}

func (p *PodResourceExecutorDocker) ParseRequestForCreateEvent(req *http.Request) error {
	var err error
	// parse the config wrapper
	if p.ConfigWrapper, err = generateConfigWrapperFromRequest(req); err != nil {
		return err
	}
	// confirm the hook point
	if p.ConfigWrapper.Config.Labels[types.ContainerTypeLabelKey] == types.ContainerTypeLabelSandbox {
		p.RuntimeRequestPath = config.RunPodSandbox
	} else {
		p.RuntimeRequestPath = config.CreateContainer
	}
	// generate the request
	switch p.RuntimeRequestPath {
	case config.RunPodSandbox:
		podMeta, err := p.getPodMeta(req)
		if err != nil {
			return err
		}
		labels, annotations := splitLabelsAndAnnotations(p.Config.Labels)
		p.PodSandboxInfo = &store.PodSandboxInfo{
			PodSandboxHookRequest: &v1alpha1.PodSandboxHookRequest{
				PodMeta:        podMeta,
				Labels:         labels,
				Annotations:    annotations,
				CgroupParent:   ToCriCgroupPath(p.cgroupDriver, p.HostConfig.CgroupParent),
				Resources:      HostConfigToResource(p.HostConfig),
				RuntimeHandler: "docker",
			},
		}
	case config.CreateContainer:
		podID := p.Config.Labels[types.SandboxIDLabelKey]
		podInfo := store.GetPodSandboxInfo(podID)
		if podInfo == nil {
			return fmt.Errorf("failed to get pod info %v", podID)
		}
		containerMeta, err := p.getContainerMeta(req)
		if err != nil {
			return err
		}
		_, annotations := splitLabelsAndAnnotations(p.Config.Labels)
		p.ContainerInfo = &store.ContainerInfo{
			ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
				PodMeta:              podInfo.PodMeta,
				PodResources:         podInfo.Resources,
				ContainerMata:        containerMeta,
				ContainerAnnotations: annotations,
				ContainerResources:   HostConfigToResource(p.HostConfig),
				PodAnnotations:       podInfo.Annotations,
				PodLabels:            podInfo.Labels,
				PodCgroupParent:      podInfo.CgroupParent,
			},
		}
	}
	return nil
}

func (p *PodResourceExecutorDocker) ParseRequest(request interface{}) error {
	req := request.(*http.Request)
	if regExp[CreateContainer].MatchString(req.URL.Path) {
		return p.ParseRequestForCreateEvent(req)
	}
	containerID, err := getContainerID(req.URL.Path)
	if err != nil {
		return fmt.Errorf("failed to get container id, err: %v", err)
	}

	isContainer := store.GetContainerInfo(containerID) != nil
	isPod := store.GetPodSandboxInfo(containerID) != nil
	if (isContainer && isPod) || (!isContainer && !isPod) {
		return fmt.Errorf("pod/container type conflict isPod(%v) isContainer(%v)", isPod, isContainer)
	}

	switch {
	case regExp[StopContainer].MatchString(req.URL.Path) && isPod:
		p.RuntimeRequestPath = config.StopPodSandbox
		p.PodSandboxInfo = store.GetPodSandboxInfo(containerID)
	case regExp[StopContainer].MatchString(req.URL.Path) && !isPod:
		p.RuntimeRequestPath = config.StopContainer
		p.ContainerInfo = store.GetContainerInfo(containerID)
	case regExp[StartContainer].MatchString(req.URL.Path) && isContainer:
		p.RuntimeRequestPath = config.StartContainer
		p.ContainerInfo = store.GetContainerInfo(containerID)
	case regExp[UpdateContainer].MatchString(req.URL.Path) && isContainer:
		p.RuntimeRequestPath = config.UpdateContainerResources
		p.ContainerInfo = store.GetContainerInfo(containerID)
		reqBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return err
		}
		containerConfig := &container.UpdateConfig{}
		if err := json.Unmarshal(reqBytes, containerConfig); err != nil {
			return nil
		}
		p.UpdateConfig = containerConfig
		// TODO: should update the ContainerInfo basing on containerConfig
	}
	return nil
}

func (p *PodResourceExecutorDocker) checkpointByCreateResponse(resp string, f func(string)) error {
	createResp := &container.ContainerCreateCreatedBody{}
	err := json.Unmarshal([]byte(resp), createResp)
	if err != nil {
		return err
	}
	f(createResp.ID)
	return nil
}

// ResourceCheckPoint
func (p *PodResourceExecutorDocker) ResourceCheckPoint(response interface{}) error {
	switch p.RuntimeRequestPath {
	case config.RunPodSandbox:
		return p.checkpointByCreateResponse(response.(string), func(id string) {
			store.WritePodSandboxInfo(id, p.PodSandboxInfo)
		})
	case config.CreateContainer:
		return p.checkpointByCreateResponse(response.(string), func(id string) {
			p.ContainerMata.Id = id
			store.WriteContainerInfo(id, p.ContainerInfo)
		})
	}
	return nil
}

func (p *PodResourceExecutorDocker) DeleteCheckpointIfNeed(request interface{}) error {
	switch p.RuntimeRequestPath {
	case config.StopPodSandbox:
		store.DeletePodSandboxInfo(p.id)
	case config.StopContainer:
		store.DeleteContainerInfo(p.id)
	}
	return nil
}

func (p *PodResourceExecutorDocker) reConstructRequest(req *http.Request, obj interface{}) (err error) {
	req.Body, req.ContentLength, err = constructHttpBody(obj)
	return err
}

func (p *PodResourceExecutorDocker) updatePodConfigByHookResponse(response interface{}) {
	if response == nil {
		return
	}
	resp, ok := response.(*v1alpha1.PodSandboxHookResponse)
	if !ok {
		return
	}
	if resp.Resources != nil {
		p.HostConfig = UpdateHostConfigByResource(p.HostConfig, resp.Resources)
		// TODO: should update not overwrite
		p.PodSandboxInfo.Resources = resp.Resources
	}
	p.HostConfig.CgroupParent = generateExpectedCgroupParent(p.cgroupDriver, resp.CgroupParent)
	p.PodSandboxInfo.CgroupParent = resp.CgroupParent
}

func (p *PodResourceExecutorDocker) updateContainerConfigByHookResponse(response interface{}) {
	if response == nil {
		return
	}
	resp, ok := response.(*v1alpha1.ContainerResourceHookResponse)
	if !ok {
		return
	}
	if resp.ContainerResources != nil {
		p.HostConfig = UpdateHostConfigByResource(p.HostConfig, resp.ContainerResources)
		p.ContainerInfo.ContainerResources = resp.ContainerResources
	}
	p.HostConfig.CgroupParent = generateExpectedCgroupParent(p.cgroupDriver, resp.PodCgroupParent)
	p.ContainerInfo.PodCgroupParent = resp.PodCgroupParent
}

func (p *PodResourceExecutorDocker) updateContainerConfigByUpdateHookResponse(response interface{}) {
	if response == nil {
		return
	}
	resp, ok := response.(*v1alpha1.ContainerResourceHookResponse)
	if !ok {
		return
	}
	if resp.ContainerResources != nil {
		p.ContainerResources = resp.ContainerResources
		p.UpdateConfig = UpdateUpdateConfigByResource(p.UpdateConfig, resp.ContainerResources)
	}
}

// ReConstructRequestByHookResponse update original http request by hook server's response
func (p *PodResourceExecutorDocker) ReConstructRequestByHookResponse(response interface{}, req *http.Request) error {
	switch p.RuntimeRequestPath {
	case config.RunPodSandbox:
		p.updatePodConfigByHookResponse(response)
		return p.reConstructRequest(req, p.ConfigWrapper)
	case config.CreateContainer:
		p.updateContainerConfigByHookResponse(response)
		return p.reConstructRequest(req, p.ConfigWrapper)
	case config.UpdateContainerResources:
		p.updateContainerConfigByUpdateHookResponse(response)
		return p.reConstructRequest(req, p.UpdateConfig)
	}
	return nil
}
