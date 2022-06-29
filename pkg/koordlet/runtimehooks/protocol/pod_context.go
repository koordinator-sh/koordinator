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
	"github.com/koordinator-sh/koordinator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type PodMeta struct {
	Namespace string
	Name      string
	UID       string
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
	PodMeta      PodMeta
	Labels       map[string]string
	Annotations  map[string]string
	CgroupParent string
}

func (p *PodRequest) FromProxy(req *runtimeapi.PodSandboxHookRequest) {
	p.PodMeta.FromProxy(req.PodMeta)
	p.Labels = req.GetLabels()
	p.Annotations = req.GetAnnotations()
	p.CgroupParent = req.GetCgroupParent()
}

func (p *PodRequest) FromReconciler(podMeta *statesinformer.PodMeta) {
	p.PodMeta.FromReconciler(podMeta.Pod.ObjectMeta)
	p.Labels = podMeta.Pod.Labels
	p.Annotations = podMeta.Pod.Annotations
	p.CgroupParent = util.GetPodCgroupDirWithKube(podMeta.CgroupDir)
}

type PodResponse struct {
	Resources Resources
}

type PodContext struct {
	Request  PodRequest
	Response PodResponse
}

func (p *PodResponse) ProxyDone(resp *runtimeapi.PodSandboxHookResponse) {
	if p.Resources.CPUSet != nil {
		resp.Resources.CpusetCpus = *p.Resources.CPUSet
	}
	if p.Resources.CPUShares != nil {
		resp.Resources.CpuShares = *p.Resources.CPUShares
	}
	if p.Resources.CFSQuota != nil {
		resp.Resources.CpuQuota = *p.Resources.CFSQuota
	}
}

func (p *PodContext) FromProxy(req *runtimeapi.PodSandboxHookRequest) {
	p.Request.FromProxy(req)
}

func (p *PodContext) ProxyDone(resp *runtimeapi.PodSandboxHookResponse) {
	p.injectForExt()
	p.Response.ProxyDone(resp)
}

func (p *PodContext) FromReconciler(podMeta *statesinformer.PodMeta) {
	p.Request.FromReconciler(podMeta)
}

func (p *PodContext) ReconcilerDone() {
	p.injectForExt()
	p.injectForOrigin()
}

func (p *PodContext) injectForOrigin() {
	// TODO
}

func (p *PodContext) injectForExt() {
	if p.Response.Resources.CPUBvt != nil {
		if err := injectCPUBvt(p.Request.CgroupParent, *p.Response.Resources.CPUBvt); err != nil {
			klog.Infof("set pod %v/%v bvt %v on cgroup parent %v failed, error %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.CPUBvt, p.Request.CgroupParent, err)
		} else {
			klog.V(5).Infof("set pod %v/%v bvt %v on cgroup parent %v", p.Request.PodMeta.Namespace,
				p.Request.PodMeta.Name, *p.Response.Resources.CPUBvt, p.Request.CgroupParent)
			audit.V(2).Pod(p.Request.PodMeta.Namespace, p.Request.PodMeta.Name).Reason("runtime-hooks").Message(
				"set pod bvt to %v", *p.Response.Resources.CPUBvt).Do()
		}
	}
}
