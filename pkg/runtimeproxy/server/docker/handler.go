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

func (d *RuntimeManagerDockerServer) HandleCreateContainer(ctx context.Context, wr http.ResponseWriter, req *http.Request) {
	// get create container config
	dec := runconfig.ContainerDecoder{}
	ContainerConfig, hostConfig, networkingConfig, err := dec.DecodeConfig(req.Body)
	if err != nil {
		klog.Errorf("Failed to decode docker create config, err: %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}

	// pre check
	runtimeResourceType := GetRuntimeResourceType(ContainerConfig.Labels)
	containerName := req.URL.Query().Get("name")
	tokens := strings.Split(containerName, "_")
	if len(tokens) != 6 {
		klog.Errorf("Failed to split k8s container name, containerName: %s", containerName)
		http.Error(wr, "Failed to split k8s container name", http.StatusInternalServerError)
		return
	}
	labels, annos := splitLabelsAndAnnotations(ContainerConfig.Labels)
	var podInfo *store.PodSandboxInfo
	var containerInfo *store.ContainerInfo
	var runtimeHookPath config.RuntimeRequestPath
	var hookReq interface{}
	if runtimeResourceType == resource_executor.RuntimeContainerResource {
		podID := ContainerConfig.Labels[types.SandboxIDLabelKey]
		podInfo = store.GetPodSandboxInfo(podID)
		if podInfo == nil {
			// refuse the req
			http.Error(wr, "Failed to get pod info", http.StatusInternalServerError)
			return
		}
		containerInfo = &store.ContainerInfo{
			ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
				PodMeta:      podInfo.PodMeta,
				PodResources: podInfo.Resources,
				ContainerMeta: &v1alpha1.ContainerMetadata{
					Name: tokens[1],
				},
				ContainerAnnotations: annos,
				ContainerResources:   HostConfigToResource(hostConfig),
				PodAnnotations:       podInfo.Annotations,
				PodLabels:            podInfo.Labels,
				PodCgroupParent:      podInfo.CgroupParent,
				ContainerEnvs:        splitDockerEnv(ContainerConfig.Env),
			},
		}
		runtimeHookPath = config.CreateContainer
		hookReq = containerInfo.GetContainerResourceHookRequest()
	} else {
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
	if types.SkipRuntimeHook(podInfo.Labels) {
		runtimeHookPath = config.NoneRuntimeHookPath
	}

	hookResp, err, failPolicy := d.dispatcher.Dispatch(ctx, runtimeHookPath, config.PreHook, hookReq)
	if err != nil {
		klog.Errorf("failed to call hook server %v, failPolicy: %v", err, failPolicy)
		if failPolicy == config.PolicyFail {
			http.Error(wr, err.Error(), http.StatusInternalServerError)
			return
		}
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
		cfgBody.HostConfig.CgroupParent = generateExpectedCgroupParent(d.cgroupDriver, resp.CgroupParent)
		podInfo.CgroupParent = resp.CgroupParent
	} else if hookResp != nil {
		resp := hookResp.(*v1alpha1.ContainerResourceHookResponse)
		if resp.ContainerResources != nil {
			cfgBody.HostConfig = UpdateHostConfigByResource(cfgBody.HostConfig, resp.ContainerResources)
			containerInfo.ContainerResources = resp.ContainerResources
		}
		cfgBody.HostConfig.CgroupParent = generateExpectedCgroupParent(d.cgroupDriver, resp.PodCgroupParent)
		containerInfo.PodCgroupParent = resp.PodCgroupParent
		if resp.ContainerEnvs != nil {
			cfgBody.Env = generateEnvList(resp.ContainerEnvs)
			containerInfo.ContainerEnvs = resp.ContainerEnvs
		}
	}

	// send req to docker
	nBody, err := encodeBody(cfgBody)
	if err != nil {
		klog.Errorf("failed to parse req to local store, err: %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Body = io.NopCloser(nBody)
	nBody, _ = encodeBody(cfgBody)
	newLength, _ := calculateContentLength(nBody)
	req.ContentLength = newLength
	resp := d.Direct(wr, req)

	createResp := &container.ContainerCreateCreatedBody{}
	err = json.Unmarshal([]byte(resp), createResp)
	if err != nil {
		klog.Errorf("Failed to Unmarshal create resp,  resp: %s, err: %v", resp, err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}

	if runtimeResourceType == resource_executor.RuntimePodResource {
		store.WritePodSandboxInfo(createResp.ID, podInfo)
	} else {
		containerInfo.ContainerMeta.Id = createResp.ID
		store.WriteContainerInfo(createResp.ID, containerInfo)
	}
}

func (d *RuntimeManagerDockerServer) HandleStartContainer(ctx context.Context, wr http.ResponseWriter, req *http.Request) {
	// we need to get the container id, because we need it to get info from checkpoint
	containerID, err := getContainerID(req.URL.Path)
	if err != nil {
		klog.Errorf("Failed to get container id, err: %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	containerMeta := store.GetContainerInfo(containerID)
	runtimeHookPath := config.NoneRuntimeHookPath
	var hookReq interface{}
	if containerMeta != nil {
		if !types.SkipRuntimeHook(containerMeta.PodLabels) {
			runtimeHookPath = config.StartContainer
			hookReq = containerMeta.GetContainerResourceHookRequest()
		}
	}

	// no need to care about the resp
	if _, err, failPolicy := d.dispatcher.Dispatch(ctx, runtimeHookPath, config.PreHook, hookReq); err != nil {
		klog.Errorf("failed to call pre start container hook server %v, failPolicy: %v", err, failPolicy)
		if failPolicy == config.PolicyFail {
			http.Error(wr, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	d.Direct(wr, req)

	if _, err, _ := d.dispatcher.Dispatch(ctx, runtimeHookPath, config.PostHook, hookReq); err != nil {
		klog.Errorf("failed to call post start container hook server %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (d *RuntimeManagerDockerServer) HandleStopContainer(ctx context.Context, wr http.ResponseWriter, req *http.Request) {
	containerID, err := getContainerID(req.URL.Path)
	if err != nil {
		klog.Errorf("Failed to get container id, err: %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}

	runtimeHookPath := config.NoneRuntimeHookPath
	var hookReq interface{}
	containerMeta := store.GetContainerInfo(containerID)
	if containerMeta != nil {
		if !types.SkipRuntimeHook(containerMeta.PodLabels) {
			runtimeHookPath = config.StopContainer
			hookReq = containerMeta.GetContainerResourceHookRequest()
		}
	} else {
		// sandbox container
		podInfo := store.GetPodSandboxInfo(containerID)
		if podInfo == nil {
			// kubelet will not treat not found error as error, so we need to return err msg as same with docker server to avoid pod terminating
			http.Error(wr, fmt.Sprintf("No such container: %s", containerID), http.StatusInternalServerError)
			return
		}
		if !types.SkipRuntimeHook(podInfo.Labels) {
			runtimeHookPath = config.StopPodSandbox
			hookReq = podInfo.GetPodSandboxHookRequest()
		}
	}

	d.Direct(wr, req)

	if containerMeta != nil {
		store.DeleteContainerInfo(containerID)
	} else {
		store.DeletePodSandboxInfo(containerID)
	}

	if _, err, _ := d.dispatcher.Dispatch(ctx, runtimeHookPath, config.PostHook, hookReq); err != nil {
		klog.Errorf("Failed to call post stop hook server %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (d *RuntimeManagerDockerServer) HandleUpdateContainer(ctx context.Context, wr http.ResponseWriter, req *http.Request) {
	containerID, err := getContainerID(req.URL.Path)
	if err != nil {
		klog.Errorf("Failed to get container id, err: %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}

	reqBytes, err := io.ReadAll(req.Body)
	if err != nil {
		klog.Errorf("Failed to ready req body, err: %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	containerConfig := &container.UpdateConfig{}
	if err := json.Unmarshal(reqBytes, containerConfig); err != nil {
		klog.Errorf("Failed to Unmarshal req body to docker config, err: %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}

	var hookReq interface{}
	containerMeta := store.GetContainerInfo(containerID)
	runtimeHookPath := config.NoneRuntimeHookPath
	if containerMeta != nil {
		if !types.SkipRuntimeHook(containerMeta.PodLabels) {
			runtimeHookPath = config.UpdateContainerResources
			hookReq = containerMeta.GetContainerResourceHookRequest()
		}
	}

	// update resources in cache with UpdateConfig
	if containerConfig != nil && hookReq != nil {
		if updateReq, ok := hookReq.(*v1alpha1.ContainerResourceHookRequest); ok {
			updateReq.ContainerResources = MergeResourceByUpdateConfig(updateReq.ContainerResources, containerConfig)
		}
	}

	response, err, failPolicy := d.dispatcher.Dispatch(ctx, runtimeHookPath, config.PreHook, hookReq)
	if err != nil {
		klog.Errorf("failed to call pre update hook server %v, failPolicy: %v", err, failPolicy)
		if failPolicy == config.PolicyFail {
			http.Error(wr, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if containerMeta != nil && response != nil {
		resp := response.(*v1alpha1.ContainerResourceHookResponse)
		if resp.ContainerResources != nil {
			containerMeta.ContainerResources = resp.ContainerResources
			containerConfig = UpdateUpdateConfigByResource(containerConfig, resp.ContainerResources)
			store.WriteContainerInfo(containerID, containerMeta)
		}
	}

	// send req to docker
	nBody, err := encodeBody(containerConfig)
	if err != nil {
		klog.Errorf("Failed to parse req to local store, err: %v", err)
		http.Error(wr, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Body = io.NopCloser(nBody)
	nBody, _ = encodeBody(containerConfig)
	newLength, _ := calculateContentLength(nBody)
	req.ContentLength = newLength

	d.Direct(wr, req)
}
