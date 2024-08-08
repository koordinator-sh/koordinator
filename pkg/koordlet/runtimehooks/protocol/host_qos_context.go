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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

type HostAppRequest struct {
	Name         string
	QOSClass     ext.QoSClass
	CgroupParent string
}

func (r *HostAppRequest) FromReconciler(hostAppSpec *slov1alpha1.HostApplicationSpec) {
	r.Name = hostAppSpec.Name
	r.QOSClass = hostAppSpec.QoS
	r.CgroupParent = util.GetHostAppCgroupRelativePath(hostAppSpec)
}

type HostAppResponse struct {
	Resources Resources
}

type HostAppContext struct {
	Request  HostAppRequest
	Response HostAppResponse
	executor resourceexecutor.ResourceUpdateExecutor
	updaters []resourceexecutor.ResourceUpdater
}

func (c *HostAppContext) RecordEvent(r record.EventRecorder, pod *corev1.Pod) {
	//TODO: don't support record pod by host level
}

func (c *HostAppContext) FromReconciler(hostAppSpec *slov1alpha1.HostApplicationSpec) {
	c.Request.FromReconciler(hostAppSpec)
}

func (c *HostAppContext) ReconcilerProcess(executor resourceexecutor.ResourceUpdateExecutor) {
	if c.executor == nil {
		c.executor = executor
	}
	c.injectForOrigin()
	c.injectForExt()
}

func (c *HostAppContext) ReconcilerDone(executor resourceexecutor.ResourceUpdateExecutor) {
	c.ReconcilerProcess(executor)
	c.Update()
}

func (c *HostAppContext) GetUpdaters() []resourceexecutor.ResourceUpdater {
	return c.updaters
}

func (c *HostAppContext) Update() {
	klog.V(5).Infof("")
	c.executor.UpdateBatch(true, c.updaters...)
	c.updaters = nil
}

func (c *HostAppContext) injectForOrigin() {
	// If CPUSet is not nil and is not an empty string, set cpuset
	if c.Response.Resources.CPUSet != nil && *c.Response.Resources.CPUSet != "" {
		eventHelper := audit.V(3).Group(c.Request.Name).Reason("runtime-hooks").Message(
			"set host application cpuset to %v", *c.Response.Resources.CPUSet)
		updater, err := injectCPUSet(c.Request.CgroupParent, *c.Response.Resources.CPUSet, eventHelper, c.executor)
		if err != nil {
			klog.Infof("set host application %v cpuset %v on cgroup parent %v failed, error %v",
				c.Request.Name, *c.Response.Resources.CPUSet, c.Request.CgroupParent, err)
		} else {
			c.updaters = append(c.updaters, updater)
			klog.V(5).Infof("set host application %v cpuset %v on cgroup parent %v",
				c.Request.Name, *c.Response.Resources.CPUSet, c.Request.CgroupParent)
		}
	}
}

func (c *HostAppContext) injectForExt() {
	if c.Response.Resources.CPUBvt != nil {
		eventHelper := audit.V(3).Group(c.Request.Name).Reason("runtime-hooks").Message(
			"set host application bvt to %v", *c.Response.Resources.CPUBvt)
		updater, err := injectCPUBvt(c.Request.CgroupParent, *c.Response.Resources.CPUBvt, eventHelper, c.executor)
		if err != nil {
			klog.Infof("set host application %v bvt %v on cgroup parent %v failed, error %v", c.Request.Name,
				*c.Response.Resources.CPUBvt, c.Request.CgroupParent, err)
		} else {
			c.updaters = append(c.updaters, updater)
			klog.V(5).Infof("set host application %v bvt %v on cgroup parent %v", c.Request.Name,
				*c.Response.Resources.CPUBvt, c.Request.CgroupParent)
		}
	}
}
