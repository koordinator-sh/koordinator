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
	"k8s.io/klog/v2"

	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type ContainerMeta struct {
	Name string
	UID  string
}

func (c *ContainerMeta) FromProxy(meta *runtimeapi.ContainerMetadata) {
	c.Name = meta.GetName()
	c.UID = meta.GetId()
}

type ContainerRequest struct {
	PodMeta        PodMeta
	ContainerMeta  ContainerMeta
	PodLabels      map[string]string
	PodAnnotations map[string]string
	CgroupParent   string
}

func (c *ContainerRequest) FromProxy(req *runtimeapi.ContainerResourceHookRequest) {
	c.PodMeta.FromProxy(req.PodMeta)
	c.ContainerMeta.FromProxy(req.ContainerMata)
	c.PodLabels = req.GetPodLabels()
	c.PodAnnotations = req.GetPodAnnotations()
	c.CgroupParent, _ = util.GetContainerCgroupPathWithKubeByID(req.GetPodCgroupParent(), req.ContainerMata.Id)
}

func (c *ContainerRequest) FromReconciler(podMeta *statesinformer.PodMeta, containerID string) {
	c.PodMeta.FromReconciler(podMeta.Pod.ObjectMeta)
	c.ContainerMeta.UID = containerID
	for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
		if containerStat.ContainerID == containerID {
			c.ContainerMeta.Name = containerStat.Name
		}
	}
	c.PodLabels = podMeta.Pod.Labels
	c.PodAnnotations = podMeta.Pod.Annotations
	c.CgroupParent, _ = util.GetContainerCgroupPathWithKubeByID(podMeta.CgroupDir, containerID)
}

type ContainerResponse struct {
	Resources Resources
}

func (c *ContainerResponse) ProxyDone(resp *runtimeapi.ContainerResourceHookResponse) {
	if c.Resources.CPUSet != nil {
		resp.ContainerResources.CpusetCpus = *c.Resources.CPUSet
	}
	if c.Resources.CFSQuota != nil {
		resp.ContainerResources.CpuQuota = *c.Resources.CFSQuota
	}
	if c.Resources.CPUShares != nil {
		resp.ContainerResources.CpuShares = *c.Resources.CPUShares
	}
}

type ContainerContext struct {
	Request  ContainerRequest
	Response ContainerResponse
}

func (c *ContainerContext) FromProxy(req *runtimeapi.ContainerResourceHookRequest) {
	c.Request.FromProxy(req)
}

func (c *ContainerContext) ProxyDone(resp *runtimeapi.ContainerResourceHookResponse) {
	c.injectForExt()
	c.Response.ProxyDone(resp)
}

func (c *ContainerContext) FromReconciler(podMeta *statesinformer.PodMeta, containerUID string) {
	c.Request.FromReconciler(podMeta, containerUID)
}

func (c *ContainerContext) ReconcilerDone() {
	c.injectForExt()
	c.injectForOrigin()
}

func (c *ContainerContext) injectForOrigin() {
	if c.Response.Resources.CPUSet != nil {
		if err := injectCPUSet(c.Request.CgroupParent, *c.Response.Resources.CPUSet); err != nil {
			klog.Infof("set container %v/%v/%v cpuset %v on cgroup parent %v failed, error %v", c.Request.PodMeta.Namespace,
				c.Request.PodMeta.Name, *c.Response.Resources.CPUSet, c.Request.CgroupParent, err)
		} else {
			klog.V(5).Infof("set container %v/%v/%v cpuset %v on cgroup parent %v",
				c.Request.PodMeta.Namespace, c.Request.PodMeta.Name, c.Request.ContainerMeta.Name,
				*c.Response.Resources.CPUSet, c.Request.CgroupParent)
			audit.V(2).Container(c.Request.ContainerMeta.UID).Reason("runtime-hooks").Message(
				"set container cpuset to %v", *c.Response.Resources.CPUSet).Do()
		}
	}
	// TODO other fields
}

func (c *ContainerContext) injectForExt() {
	// TODO
}
