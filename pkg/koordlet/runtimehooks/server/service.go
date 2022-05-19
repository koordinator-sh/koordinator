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

package server

import (
	"context"

	"k8s.io/klog/v2"

	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtime-manager/config"

	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
)

func (s *server) PreRunPodSandboxHook(ctx context.Context,
	req *runtimeapi.RunPodSandboxHookRequest) (*runtimeapi.RunPodSandboxHookResponse, error) {
	klog.V(5).Infof("receive PreRunPodSandboxHook request %v", req.String())
	resp := &runtimeapi.RunPodSandboxHookResponse{
		Labels:       req.Labels,
		Annotations:  req.Annotations,
		CgroupParent: req.CgroupParent,
		Resources:    req.Resources,
	}
	hooks.RunHooks(rmconfig.PreRunPodSandbox, req, resp)
	return resp, nil
}

func (s *server) PreStartContainerHook(ctx context.Context,
	req *runtimeapi.ContainerResourceHookRequest) (*runtimeapi.ContainerResourceHookResponse, error) {
	klog.V(5).Infof("receive PreStartContainerHook request %v", req.String())
	resp := &runtimeapi.ContainerResourceHookResponse{
		ContainerAnnotations: req.ContainerAnnotations,
		ContainerResources:   req.ContainerResources,
	}
	hooks.RunHooks(rmconfig.PreStartContainer, req, resp)
	return resp, nil
}

func (s *server) PostStartContainerHook(ctx context.Context,
	req *runtimeapi.ContainerResourceHookRequest) (*runtimeapi.ContainerResourceHookResponse, error) {
	klog.V(5).Infof("receive PostStartContainerHook request %v", req.String())
	resp := &runtimeapi.ContainerResourceHookResponse{
		ContainerAnnotations: req.ContainerAnnotations,
		ContainerResources:   req.ContainerResources,
	}
	hooks.RunHooks(rmconfig.PostStartContainer, req, resp)
	return resp, nil
}

func (s *server) PostStopContainerHook(ctx context.Context,
	req *runtimeapi.ContainerResourceHookRequest) (*runtimeapi.ContainerResourceHookResponse, error) {
	klog.V(5).Infof("receive PostStopContainerHook request %v", req.String())
	resp := &runtimeapi.ContainerResourceHookResponse{
		ContainerAnnotations: req.ContainerAnnotations,
		ContainerResources:   req.ContainerResources,
	}
	hooks.RunHooks(rmconfig.PostStopContainer, req, resp)
	return resp, nil
}

func (s *server) PreUpdateContainerResourcesHook(ctx context.Context,
	req *runtimeapi.ContainerResourceHookRequest) (*runtimeapi.ContainerResourceHookResponse, error) {
	klog.V(5).Infof("receive PreUpdateContainerResourcesHook request %v", req.String())
	resp := &runtimeapi.ContainerResourceHookResponse{
		ContainerAnnotations: req.ContainerAnnotations,
		ContainerResources:   req.ContainerResources,
	}
	hooks.RunHooks(rmconfig.PreUpdateContainerResources, req, resp)
	return resp, nil
}
