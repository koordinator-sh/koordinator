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

package protocol

import (
	"fmt"
	"os"
	"sort"

	"github.com/containerd/nri/pkg/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	recutil "k8s.io/client-go/tools/record/util"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type PodMeta struct {
	Namespace string
	Name      string
	UID       string
}

func (p *PodMeta) String() string {
	return fmt.Sprintf("%v/%v", p.Namespace, p.Name)
}

func (p *PodMeta) FromNri(pod *api.PodSandbox) {
	p.Namespace = pod.GetNamespace()
	p.Name = pod.GetName()
	p.UID = pod.GetUid()
}

func (p *PodMeta) FromProxy(meta *runtimeapi.PodSandboxMetadata) {
	p.Namespace = meta.GetNamespace()
	p.Name = meta.GetName()
	p.UID = meta.GetUid()
}

func (p *PodMeta) FromReconciler(meta metav1.ObjectMeta) {
	p.Namespace = meta.Namespace
	p.Name = meta.Name
	p.UID = string(meta.UID)
}

type PodRequest struct {
	PodMeta           PodMeta
	Labels            map[string]string
	Annotations       map[string]string
	CgroupParent      string
	Resources         *Resources // TODO: support proxy & nri mode
	ExtendedResources *apiext.ExtendedResourceSpec
	ContainerTaskIds  map[string][]int32
}

func (p *PodRequest) FromNri(pod *api.PodSandbox) {
	p.PodMeta.FromNri(pod)
	p.Labels = pod.GetLabels()
	p.Annotations = pod.GetAnnotations()
	p.CgroupParent = pod.GetLinux().GetCgroupParent()
	// retrieve ExtendedResources from pod annotations
	spec, err := apiext.GetExtendedResourceSpec(pod.GetAnnotations())
	if err != nil {
		klog.V(4).Infof("failed to get ExtendedResourceSpec from nri via annotation, pod %s/%s, err: %s",
			p.PodMeta.Namespace, p.PodMeta.Name, err)
	}
	if spec != nil && spec.Containers != nil {
		p.ExtendedResources = spec
	}
}

func (p *PodRequest) FromProxy(req *runtimeapi.PodSandboxHookRequest) {
	p.PodMeta.FromProxy(req.PodMeta)
	p.Labels = req.GetLabels()
	p.Annotations = req.GetAnnotations()
	p.CgroupParent = req.GetCgroupParent()
	// retrieve ExtendedResources from pod annotations
	spec, err := apiext.GetExtendedResourceSpec(req.GetAnnotations())
	if err != nil {
		klog.V(4).Infof("failed to get ExtendedResourceSpec from proxy via annotation, pod %s/%s, err: %s",
			p.PodMeta.Namespace, p.PodMeta.Name, err)
	}
	if spec != nil && spec.Containers != nil {
		p.ExtendedResources = spec
	}
}

func (p *PodRequest) FromReconciler(podMeta *statesinformer.PodMeta) {
	p.PodMeta.FromReconciler(podMeta.Pod.ObjectMeta)
	p.Labels = podMeta.Pod.Labels
	p.Annotations = podMeta.Pod.Annotations
	p.CgroupParent = podMeta.CgroupDir
	p.ContainerTaskIds = podMeta.ContainerTaskIds
	p.Resources = &Resources{}
	p.Resources.FromPod(podMeta.Pod)
	// retrieve ExtendedResources from pod spec and pod annotations (prefer pod spec)
	specFromAnnotations, err := apiext.GetExtendedResourceSpec(podMeta.Pod.Annotations)
	if err != nil {
		klog.V(4).Infof("failed to get ExtendedResourceSpec from reconciler via annotation, pod %s/%s, err: %s",
			p.PodMeta.Namespace, p.PodMeta.Name, err)
	}
	specFromPod := util.GetPodExtendedResources(podMeta.Pod)
	if specFromPod != nil {
		p.ExtendedResources = specFromPod
	} else if specFromAnnotations != nil && specFromAnnotations.Containers != nil { // specFromPod == nil
		p.ExtendedResources = specFromAnnotations
	}
}

type RecorderEvent struct {
	HookName  string
	MsgFmt    string
	Reason    string
	EventType string
}

type PodResponse struct {
	Resources Resources
}

type PodContext struct {
	Request        PodRequest
	Response       PodResponse
	executor       resourceexecutor.ResourceUpdateExecutor
	updaters       []resourceexecutor.ResourceUpdater
	RecorderEvents []RecorderEvent
}

func (p *PodContext) RecordEvent(r record.EventRecorder, pod *corev1.Pod) {
	// Noraml, Warning => RecordEvent
	events := make(map[string]RecorderEvent)
	for _, event := range p.RecorderEvents {
		if !recutil.ValidateEventType(event.EventType) {
			klog.Warningf("EventType is not valid %v", event)
			continue
		}

		e := event
		if _, ok := events[event.EventType]; ok {
			e.MsgFmt += "-" + event.MsgFmt
			e.Reason += "-" + event.Reason
		}
		events[event.EventType] = e
	}

	eventTypes := make([]string, 0, len(events))
	for eventType := range events {
		eventTypes = append(eventTypes, eventType)
	}
	sort.Strings(eventTypes)

	for _, eventType := range eventTypes {
		event := events[eventType]
		r.Eventf(pod, eventType, event.Reason, event.MsgFmt)
	}
}

func (p *PodResponse) ProxyDone(resp *runtimeapi.PodSandboxHookResponse) {
	if p.Resources.IsOriginResSet() && resp.Resources == nil {
		// resource value is injected but origin request is nil, init resource response
		resp.Resources = &runtimeapi.LinuxContainerResources{}
	}
	if p.Resources.CPUSet != nil {
		resp.Resources.CpusetCpus = *p.Resources.CPUSet
	}
	if p.Resources.CPUShares != nil {
		resp.Resources.CpuShares = *p.Resources.CPUShares
	}
	if p.Resources.CFSQuota != nil {
		resp.Resources.CpuQuota = *p.Resources.CFSQuota
	}
	if p.Resources.MemoryLimit != nil {
		resp.Resources.MemoryLimitInBytes = *p.Resources.MemoryLimit
	}
}

func (p *PodContext) FromNri(pod *api.PodSandbox) {
	p.Request.FromNri(pod)
}

func (p *PodContext) FromProxy(req *runtimeapi.PodSandboxHookRequest) {
	p.Request.FromProxy(req)
}

func (p *PodContext) ProxyDone(resp *runtimeapi.PodSandboxHookResponse, executor resourceexecutor.ResourceUpdateExecutor) {
	if p.executor == nil {
		p.executor = executor
	}
	p.injectForExt()
	p.Response.ProxyDone(resp)
	p.Update()
}

func (p *PodContext) NriDone(executor resourceexecutor.ResourceUpdateExecutor) {
	if p.executor == nil {
		p.executor = executor
	}
	p.injectForExt()
	p.Update()
}

func (p *PodContext) NriRemoveDone(executor resourceexecutor.ResourceUpdateExecutor) {
	if p.executor == nil {
		p.executor = executor
	}
	p.removeForExt()
	p.Update()
}

func (p *PodContext) FromReconciler(podMeta *statesinformer.PodMeta) {
	p.Request.FromReconciler(podMeta)
}

// ReconcilerProcess generate the resource updaters but not do the update until the Update() is called.
func (p *PodContext) ReconcilerProcess(executor resourceexecutor.ResourceUpdateExecutor) {
	if p.executor == nil {
		p.executor = executor
	}
	p.injectForExt()
	p.injectForOrigin()
}

func (p *PodContext) ReconcilerDone(executor resourceexecutor.ResourceUpdateExecutor) {
	p.ReconcilerProcess(executor)
	p.Update()
}

func (p *PodContext) GetUpdaters() []resourceexecutor.ResourceUpdater {
	return p.updaters
}

func (p *PodContext) Update() {
	p.executor.UpdateBatch(true, p.updaters...)
	p.updaters = nil
}

func (p *PodContext) injectForOrigin() {
	// TODO
}

func (p *PodContext) injectForExt() {
	if p.Response.Resources.CPUBvt != nil {
		eventHelper := audit.V(3).Pod(p.Request.PodMeta.Namespace, p.Request.PodMeta.Name).Reason("runtime-hooks").Message(
			"set pod bvt to %v", *p.Response.Resources.CPUBvt)
		updater, err := injectCPUBvt(p.Request.CgroupParent, *p.Response.Resources.CPUBvt, eventHelper, p.executor)
		if err != nil {
			klog.Infof("set pod %v/%v bvt %v on cgroup parent %v failed, error %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.CPUBvt, p.Request.CgroupParent, err)
		} else {
			p.updaters = append(p.updaters, updater)
			klog.V(5).Infof("set pod %v/%v bvt %v on cgroup parent %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.CPUBvt, p.Request.CgroupParent)
		}
	}
	if p.Response.Resources.CPUIdle != nil {
		eventHelper := audit.V(3).Pod(p.Request.PodMeta.Namespace, p.Request.PodMeta.Name).Reason("runtime-hooks").Message(
			"set pod idle to %v", *p.Response.Resources.CPUIdle)
		updater, err := injectCPUIdle(p.Request.CgroupParent, *p.Response.Resources.CPUIdle, eventHelper, p.executor)
		if err != nil {
			klog.Infof("set pod %v/%v idle %v on cgroup parent %v failed, error %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.CPUIdle, p.Request.CgroupParent, err)
		} else {
			p.updaters = append(p.updaters, updater)
			klog.V(5).Infof("set pod %v/%v idle %v on cgroup parent %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.CPUIdle, p.Request.CgroupParent)
		}
	}

	// some of pod-level cgroups are manually updated since pod-stage hooks do not support it;
	// kubelet may set the cgroups when pod is created or restarted, so we need to update the cgroups repeatedly
	if p.Response.Resources.CPUShares != nil {
		eventHelper := audit.V(3).Pod(p.Request.PodMeta.Namespace, p.Request.PodMeta.Name).Reason("runtime-hooks").Message(
			"set pod cpu shares to %v", *p.Response.Resources.CPUShares)
		updater, err := injectCPUShares(p.Request.CgroupParent, *p.Response.Resources.CPUShares, eventHelper, p.executor)
		if err != nil {
			klog.Infof("set pod %v/%v cpu shares %v on cgroup parent %v failed, error %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.CPUShares, p.Request.CgroupParent, err)
		} else {
			p.updaters = append(p.updaters, updater)
			klog.V(5).Infof("set pod %v/%v cpu shares %v on cgroup parent %v",
				p.Request.PodMeta.Namespace, p.Request.PodMeta.Name, *p.Response.Resources.CPUShares, p.Request.CgroupParent)
		}
	}
	if p.Response.Resources.CFSQuota != nil {
		eventHelper := audit.V(3).Pod(p.Request.PodMeta.Namespace, p.Request.PodMeta.Name).Reason("runtime-hooks").Message(
			"set pod cfs quota to %v", *p.Response.Resources.CFSQuota)
		updater, err := injectCPUQuota(p.Request.CgroupParent, *p.Response.Resources.CFSQuota, eventHelper, p.executor)
		if err != nil {
			klog.Infof("set pod %v/%v cfs quota %v on cgroup parent %v failed, error %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.CFSQuota, p.Request.CgroupParent, err)
		} else {
			p.updaters = append(p.updaters, updater)
			klog.V(5).Infof("set pod %v/%v cfs quota %v on cgroup parent %v",
				p.Request.PodMeta.Namespace, p.Request.PodMeta.Name, *p.Response.Resources.CFSQuota, p.Request.CgroupParent)
		}
	}
	if p.Response.Resources.MemoryLimit != nil {
		eventHelper := audit.V(3).Pod(p.Request.PodMeta.Namespace, p.Request.PodMeta.Name).Reason("runtime-hooks").Message(
			"set pod memory limit to %v", *p.Response.Resources.MemoryLimit)
		updater, err := injectMemoryLimit(p.Request.CgroupParent, *p.Response.Resources.MemoryLimit, eventHelper, p.executor)
		if err != nil {
			klog.Infof("set pod %v/%v memory limit %v on cgroup parent %v failed, error %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.MemoryLimit, p.Request.CgroupParent, err)
		} else {
			p.updaters = append(p.updaters, updater)
			klog.V(5).Infof("set pod %v/%v memory limit %v on cgroup parent %v",
				p.Request.PodMeta.Namespace, p.Request.PodMeta.Name, *p.Response.Resources.MemoryLimit, p.Request.CgroupParent)
		}
	}

	if p.Response.Resources.NetClsClassId != nil {
		eventHelper := audit.V(3).Pod(p.Request.PodMeta.Namespace, p.Request.PodMeta.Name).Reason("runtime-hooks").Message(
			"set pod net class id to %v", *p.Response.Resources.NetClsClassId)
		updater, err := injectNetClsClassId(p.Request.CgroupParent, *p.Response.Resources.NetClsClassId, eventHelper, p.executor)
		if err != nil {
			klog.Infof("set pod %v/%v net class id %v on cgroup parent %v failed, error %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.NetClsClassId, p.Request.CgroupParent, err)
		} else {
			p.updaters = append(p.updaters, updater)
			klog.V(5).Infof("set pod %v/%v net class id %v on cgroup parent %v",
				p.Request.PodMeta.Namespace, p.Request.PodMeta.Name, *p.Response.Resources.NetClsClassId, p.Request.CgroupParent)
		}
	}

	if p.Response.Resources.Resctrl != nil {
		eventHelper := audit.V(3).Pod(p.Request.PodMeta.Namespace, p.Request.PodMeta.Name).Reason("runtime-hooks").Message(
			"set pod LLC/MB limit to %v", *p.Response.Resources.Resctrl)
		if p.Response.Resources.Resctrl.Closid != "" || p.Response.Resources.Resctrl.Schemata != "" {
			updater, err := createCatGroup(p.Response.Resources.Resctrl.Closid, eventHelper, p.executor)
			if err != nil {
				klog.Infof("create pod %v/%v cat group %v failed, error %v", p.Request.PodMeta.Namespace,
					p.Request.PodMeta.Name, p.Response.Resources.Resctrl.Closid, err)
			} else {
				p.updaters = append(p.updaters, updater)
				klog.V(5).Infof("create pod %v/%v cat group %v",
					p.Request.PodMeta.Namespace, p.Request.PodMeta.Name, p.Response.Resources.Resctrl.Closid)
			}

			updater, err = injectResctrl(p.Response.Resources.Resctrl.Closid, p.Response.Resources.Resctrl.Schemata, eventHelper, p.executor)
			if err != nil {
				klog.Infof("set pod %v/%v LLC/MB limit %v on cgroup parent %v failed, error %v", p.Request.PodMeta.Namespace,
					p.Request.PodMeta.Name, p.Response.Resources.Resctrl.Closid, p.Response.Resources.Resctrl.Schemata, err)
			} else {
				p.updaters = append(p.updaters, updater)
				klog.V(5).Infof("set pod %v/%v LLC/MB limit %v on cgroup parent %v",
					p.Request.PodMeta.Namespace, p.Request.PodMeta.Name, *p.Response.Resources.Resctrl, p.Request.CgroupParent)
			}
		}

		if len(p.Response.Resources.Resctrl.NewTaskIds) > 0 {
			updater, err := resourceexecutor.CalculateResctrlL3TasksResource(p.Response.Resources.Resctrl.Closid, p.Response.Resources.Resctrl.NewTaskIds)
			if err != nil {
				klog.V(5).Infof("failed to get l3 tasks resource for group %s, err: %s", p.Response.Resources.Resctrl.Closid, err)
			} else {
				p.updaters = append(p.updaters, updater)
			}
		}
	}
}

func (p *PodContext) removeForExt() {
	if p.Response.Resources.Resctrl != nil && p.Response.Resources.Resctrl.Closid != "" {
		if err := os.Remove(system.GetResctrlGroupRootDirPath(p.Response.Resources.Resctrl.Closid)); err != nil {
			klog.Infof("cannot remove ctrl group, err: %v", err)
		} else {
			klog.Infof("remove pod %v/%v ctrl group %v on cgroup parent %v",
				p.Request.PodMeta.Namespace, p.Request.PodMeta.Name, *p.Response.Resources.Resctrl, p.Request.CgroupParent)

		}
	}
}
