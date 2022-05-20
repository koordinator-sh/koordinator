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

type PodResourceExecutor struct {
	store          *store.MetaManager
	PodMeta        *v1alpha1.PodSandboxMetadata
	RuntimeHandler string
	Annotations    map[string]string
	Labels         map[string]string
	CgroupParent   string
}

func NewPodResourceExecutor(store *store.MetaManager) *PodResourceExecutor {
	return &PodResourceExecutor{
		store: store,
	}
}

func (p *PodResourceExecutor) String() string {
	return fmt.Sprintf("%v/%v", p.PodMeta.Name, p.PodMeta.Uid)
}

func (p *PodResourceExecutor) GetMetaInfo() string {
	return fmt.Sprintf("%v/%v", p.PodMeta.Name, p.PodMeta.Uid)
}

func (p *PodResourceExecutor) GenerateResourceCheckpoint() interface{} {
	return &store.PodSandboxInfo{
		PodMeta:        p.PodMeta,
		RuntimeHandler: p.RuntimeHandler,
		Annotations:    p.Annotations,
		Labels:         p.Labels,
	}
}

func (p *PodResourceExecutor) GenerateHookRequest() interface{} {
	return &v1alpha1.RunPodSandboxHookRequest{
		PodMeta:        p.PodMeta,
		RuntimeHandler: p.RuntimeHandler,
		Annotations:    p.Annotations,
		Labels:         p.Labels,
	}
}

func (p *PodResourceExecutor) updateByCheckPoint(podID string) error {
	podCheckPoint := p.store.GetPodSandboxInfo(podID)
	if podCheckPoint == nil {
		return fmt.Errorf("no pod item related to %v", podID)
	}
	p.PodMeta = podCheckPoint.PodMeta
	p.RuntimeHandler = podCheckPoint.RuntimeHandler
	p.Annotations = podCheckPoint.Annotations
	p.Labels = podCheckPoint.Labels
	p.CgroupParent = podCheckPoint.CgroupParent
	klog.Infof("get pod info successful %v", podID)
	return nil
}

func (p *PodResourceExecutor) ParseRequest(req interface{}) error {
	switch request := req.(type) {
	case *runtimeapi.RunPodSandboxRequest:
		p.PodMeta = &v1alpha1.PodSandboxMetadata{
			Name:      request.GetConfig().GetMetadata().GetName(),
			Namespace: request.GetConfig().GetMetadata().GetNamespace(),
		}
		p.RuntimeHandler = request.GetRuntimeHandler()
		p.Annotations = request.GetConfig().GetAnnotations()
		p.Labels = request.GetConfig().GetLabels()
		p.CgroupParent = request.GetConfig().GetLinux().GetCgroupParent()
		klog.Infof("success parse pod Info %v during pod run", p)
	case *runtimeapi.StopPodSandboxRequest:
		return p.updateByCheckPoint(request.GetPodSandboxId())

	}
	return nil
}

func (p *PodResourceExecutor) ParsePod(podsandbox *runtimeapi.PodSandbox) error {
	if p == nil {
		return nil
	}
	p.PodMeta = &v1alpha1.PodSandboxMetadata{
		Name:      podsandbox.GetMetadata().GetName(),
		Namespace: podsandbox.GetMetadata().GetNamespace(),
	}
	p.RuntimeHandler = podsandbox.GetRuntimeHandler()
	p.Annotations = podsandbox.GetAnnotations()
	p.Labels = podsandbox.GetLabels()
	// TODO: how to get cgroup parent when failOver
	return nil
}

func (p *PodResourceExecutor) ResourceCheckPoint(response interface{}) error {
	runPodSandboxResponse, ok := response.(*runtimeapi.RunPodSandboxResponse)
	if !ok {
		return fmt.Errorf("bad response %v", response)
	}
	podCheckPoint := p.GenerateResourceCheckpoint().(*store.PodSandboxInfo)
	data, _ := json.Marshal(podCheckPoint)
	err := p.store.WritePodSandboxInfo(runPodSandboxResponse.PodSandboxId, podCheckPoint)
	if err != nil {
		return err
	}
	klog.Infof("success to checkpoint pod level info %v %v",
		runPodSandboxResponse.PodSandboxId, string(data))
	return nil
}

func (p *PodResourceExecutor) DeleteCheckpointIfNeed(req interface{}) error {
	switch request := req.(type) {
	case *runtimeapi.StopPodSandboxRequest:
		p.store.DeletePodSandboxInfo(request.GetPodSandboxId())
	}
	return nil
}

func (p *PodResourceExecutor) UpdateResource(rsp interface{}) error {
	switch response := rsp.(type) {
	case *v1alpha1.RunPodSandboxHookResponse:
		p.Annotations = utils.MergeMap(p.Annotations, response.Annotations)
		p.Labels = utils.MergeMap(p.Labels, response.Labels)
		if response.CgroupParent != "" {
			p.CgroupParent = response.CgroupParent
		}
	}
	return nil
}
