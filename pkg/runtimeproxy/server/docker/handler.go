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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/runconfig"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
	resource_executor "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/resexecutor"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/server/types"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/store"
)

func (d *RuntimeManagerDockerServer) generateErrorInfo(wr http.ResponseWriter, err error) {
	klog.Errorf("err: %v", err)
	http.Error(wr, err.Error(), http.StatusInternalServerError)
}

func (d *RuntimeManagerDockerServer) getNameTokens(strs []string) ([]string, error) {
	containerName := ""
	if len(strs) >= 1 {
		containerName = strs[0]
	}
	tokens := strings.Split(containerName, "_")
	if len(tokens) != 6 {
		return nil, fmt.Errorf("-")
	}
	return tokens, nil
}

func (d *RuntimeManagerDockerServer) HandleCreateContainer(ctx context.Context, wr http.ResponseWriter, req *http.Request) {
	// get create container config
	ContainerConfig, hostConfig, networkingConfig, err := runconfig.ContainerDecoder{}.DecodeConfig(req.Body)
	if err != nil {
		return
	}
	tokens, err := d.getNameTokens(req.URL.Query()["name"])
	if err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("failed to split k8s container name: %v", req.URL.Query()["name"]))
		return
	}
	labels, annos := splitLabelsAndAnnotations(ContainerConfig.Labels)

	var podInfo *store.PodSandboxInfo
	var containerInfo *store.ContainerInfo
	var hookReq interface{}

	runtimeResourceType := GetRuntimeResourceType(ContainerConfig.Labels)
	runtimeHookPath := config.NoneRuntimeHookPath

	switch runtimeResourceType {
	case resource_executor.RuntimeContainerResource:
		podID := ContainerConfig.Labels[types.SandboxIDLabelKey]
		podInfo := store.GetPodSandboxInfo(podID)
		if podInfo == nil {
			// refuse the req
			http.Error(wr, "Failed to get pod info", http.StatusInternalServerError)
			return
		}
		// TODO(ZYEcho): implement create container hook
		containerInfo = &store.ContainerInfo{
			ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
				PodMeta:      podInfo.PodMeta,
				PodResources: podInfo.Resources,
				ContainerMata: &v1alpha1.ContainerMetadata{
					Name: tokens[1],
				},
				ContainerAnnotations: annos,
				ContainerResources:   HostConfigToResource(hostConfig),
			},
		}
	case resource_executor.RuntimePodResource:
		runtimeHookPath = config.RunPodSandbox
		podInfo = &store.PodSandboxInfo{
			PodSandboxHookRequest: &v1alpha1.PodSandboxHookRequest{
				PodMeta: &v1alpha1.PodSandboxMetadata{
					Name:      tokens[2],
					Namespace: tokens[3],
					Uid:       tokens[4],
				},
				Labels:         labels,
				Annotations:    annos,
				CgroupParent:   ToCriCgroupPath(d.cgroupDriver, hostConfig.CgroupParent),
				Resources:      HostConfigToResource(hostConfig),
				RuntimeHandler: "docker",
			},
		}
		hookReq = podInfo.GetPodSandboxHookRequest()
	}

	hookResp, err := d.dispatcher.Dispatch(ctx, runtimeHookPath, config.PreHook, hookReq)
	if err != nil {
		klog.Errorf("Failed to call hook server %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}

	cfgBody := types.ConfigWrapper{
		Config:           ContainerConfig,
		HostConfig:       hostConfig,
		NetworkingConfig: networkingConfig,
	}

	if runtimeResourceType == resource_executor.RuntimePodResource && hookResp != nil {
		resp := hookResp.(*v1alpha1.PodSandboxHookResponse)
		if resp.Resources != nil {
			cfgBody.HostConfig = UpdateHostConfigByResource(cfgBody.HostConfig, resp.Resources)
			podInfo.Resources = resp.Resources
		}
	}

	if req.Body, req.ContentLength, err = generateNewBody(cfgBody); err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("fail to parse req to local store: %v", err))
		return
	}

	// send req to docker
	resp := d.Direct(wr, req)

	createResp := &container.ContainerCreateCreatedBody{}
	err = json.Unmarshal([]byte(resp), createResp)
	if err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("fail to unmarshal create response %v %v", resp, err))
		return
	}

	if runtimeResourceType == resource_executor.RuntimePodResource {
		store.WritePodSandboxInfo(createResp.ID, podInfo)
	} else {
		store.WriteContainerInfo(createResp.ID, containerInfo)
	}
}

func (d *RuntimeManagerDockerServer) parseContainerInfo(url string) (*store.ContainerInfo, string, error) {
	// we need to get the container id, because we need it to get info from checkpoint
	containerID, err := getContainerID(url)
	if err != nil {
		return nil, "", err
	}
	// TODO: currently to check container is sandbox/container by checking container id existence in local-store
	return store.GetContainerInfo(containerID), containerID, nil
}

func (d *RuntimeManagerDockerServer) HandleStartContainer(ctx context.Context, wr http.ResponseWriter, req *http.Request) {
	// we need to get the container id, because we need it to get info from checkpoint
	containerMeta, _, err := d.parseContainerInfo(req.URL.Path)
	if err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("failed to get container id, err: %v", err))
		return
	}

	// no need to care about the resp
	if _, err := d.dispatcher.Dispatch(ctx, config.StartContainer, config.PreHook, containerMeta.GetContainerResourceHookRequest()); err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("failed to call pre start container hook server %v", err))
		return
	}

	d.Direct(wr, req)

	if _, err := d.dispatcher.Dispatch(ctx, config.StartContainer, config.PostHook, containerMeta.GetContainerResourceHookRequest()); err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("failed to call post start container hook server %v", err))
		return
	}
}

func (d *RuntimeManagerDockerServer) HandleStopContainer(ctx context.Context, wr http.ResponseWriter, req *http.Request) {
	containerMeta, containerID, err := d.parseContainerInfo(req.URL.Path)
	if err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("failed to get container id %v", err))
		return
	}

	runtimeHookPath := config.NoneRuntimeHookPath
	var hookReq interface{}
	if containerMeta != nil {
		runtimeHookPath = config.StopContainer
		hookReq = containerMeta.GetContainerResourceHookRequest()
	}

	d.Direct(wr, req)

	// TODO:
	if containerMeta != nil {
		store.DeleteContainerInfo(containerID)
	} else {
		store.DeletePodSandboxInfo(containerID)
	}

	if _, err := d.dispatcher.Dispatch(ctx, runtimeHookPath, config.PostHook, hookReq); err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("failed to call post stop hook server %v", err))
		return
	}
}

func (d *RuntimeManagerDockerServer) HandleUpdateContainer(ctx context.Context, wr http.ResponseWriter, req *http.Request) {
	containerMeta, containerID, err := d.parseContainerInfo(req.URL.Path)
	if err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("failed to get container id %v", err))
		return
	}

	reqBytes, err := io.ReadAll(req.Body)
	if err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("failed to get container id %v", err))
		return
	}
	containerConfig := &container.UpdateConfig{}
	if err := json.Unmarshal(reqBytes, containerConfig); err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("fail to Unmarshal req body to docker config, err: %v", err))
		return
	}

	response, err := d.dispatcher.Dispatch(ctx, config.UpdateContainerResources, config.PreHook, containerMeta.GetContainerResourceHookRequest())
	if err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("fail to pre update hook: %v", err))
		return
	}

	if containerMeta != nil && response != nil {
		resp := response.(*v1alpha1.ContainerResourceHookResponse)
		if resp.ContainerResources != nil {
			containerMeta.ContainerResources = resp.ContainerResources
			UpdateUpdateConfigByResource(containerConfig, resp.ContainerResources)
			store.WriteContainerInfo(containerID, containerMeta)
		}
	}

	if req.Body, req.ContentLength, err = generateNewBody(containerConfig); err != nil {
		d.generateErrorInfo(wr, fmt.Errorf("fail to parse req to local store: %v", err))
		return
	}

	d.Direct(wr, req)
}
