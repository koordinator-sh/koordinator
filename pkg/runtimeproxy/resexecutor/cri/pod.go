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

type PodResourceExecutor struct {
	store.PodSandboxInfo
}

func NewPodResourceExecutor() *PodResourceExecutor {
	return &PodResourceExecutor{}
}

func (p *PodResourceExecutor) String() string {
	return fmt.Sprintf("%v/%v", p.GetPodMeta().GetName(), p.GetPodMeta().GetUid())
}

func (p *PodResourceExecutor) GetMetaInfo() string {
	return fmt.Sprintf("%v/%v", p.GetPodMeta().GetName(), p.GetPodMeta().GetUid())
}

func (p *PodResourceExecutor) GenerateHookRequest() interface{} {
	return p.GetRunPodSandboxHookRequest()
}

func (p *PodResourceExecutor) loadPodSandboxFromStore(podID string) error {
	podSandbox := store.GetPodSandboxInfo(podID)
	if podSandbox == nil {
		return fmt.Errorf("no pod item related to %v", podID)
	}
	p.PodSandboxInfo = *podSandbox
	klog.Infof("get pod info successful %v", podID)
	return nil
}

func (p *PodResourceExecutor) ParseRequest(req interface{}) error {
	switch request := req.(type) {
	case *runtimeapi.RunPodSandboxRequest:
		p.PodSandboxInfo = store.PodSandboxInfo{
			PodSandboxHookRequest: &v1alpha1.PodSandboxHookRequest{
				PodMeta: &v1alpha1.PodSandboxMetadata{
					Name:      request.GetConfig().GetMetadata().GetName(),
					Namespace: request.GetConfig().GetMetadata().GetNamespace(),
				},
				RuntimeHandler: request.GetRuntimeHandler(),
				Annotations:    request.GetConfig().GetAnnotations(),
				Labels:         request.GetConfig().GetLabels(),
				CgroupParent:   request.GetConfig().GetLinux().GetCgroupParent(),
			},
		}
		klog.Infof("success parse pod Info %v during pod run", p)
	case *runtimeapi.StopPodSandboxRequest:
		return p.loadPodSandboxFromStore(request.GetPodSandboxId())
	}
	return nil
}

func (p *PodResourceExecutor) ParsePod(podsandbox *runtimeapi.PodSandbox) error {
	if p == nil {
		return nil
	}
	p.PodSandboxInfo = store.PodSandboxInfo{
		PodSandboxHookRequest: &v1alpha1.PodSandboxHookRequest{
			PodMeta: &v1alpha1.PodSandboxMetadata{
				Name:      podsandbox.GetMetadata().GetName(),
				Namespace: podsandbox.GetMetadata().GetNamespace(),
			},
			RuntimeHandler: podsandbox.GetRuntimeHandler(),
			Annotations:    podsandbox.GetAnnotations(),
			Labels:         podsandbox.GetLabels(),
			// TODO: how to get cgroup parent when failOver
		},
	}
	return nil
}

func (p *PodResourceExecutor) ResourceCheckPoint(response interface{}) error {
	runPodSandboxResponse, ok := response.(*runtimeapi.RunPodSandboxResponse)
	if !ok || p.GetRunPodSandboxHookRequest() == nil {
		return fmt.Errorf("no need to checkpoint resource %v %v", response, p.GetRunPodSandboxHookRequest())
	}
	err := store.WritePodSandboxInfo(runPodSandboxResponse.PodSandboxId, &p.PodSandboxInfo)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(p.PodSandboxInfo)
	klog.Infof("success to checkpoint pod level info %v %v",
		runPodSandboxResponse.PodSandboxId, string(data))
	return nil
}

func (p *PodResourceExecutor) DeleteCheckpointIfNeed(req interface{}) error {
	switch request := req.(type) {
	case *runtimeapi.StopPodSandboxRequest:
		store.DeletePodSandboxInfo(request.GetPodSandboxId())
	}
	return nil
}

func (p *PodResourceExecutor) UpdateResource(rsp interface{}) error {
	switch response := rsp.(type) {
	case *v1alpha1.PodSandboxHookResponse:
		p.Annotations = utils.MergeMap(p.Annotations, response.Annotations)
		p.Labels = utils.MergeMap(p.Labels, response.Labels)
		if response.CgroupParent != "" {
			p.CgroupParent = response.CgroupParent
		}
	}
	return nil
}
