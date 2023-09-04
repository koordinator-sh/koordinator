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
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

const (
	hostLSCgroupDir = "host-latency-sensitive"
	hostBECgroupDir = "host-best-effort"
)

func getHostCgroupRelativePath(hostAppSpec *slov1alpha1.HostApplicationSpec) string {
	if hostAppSpec == nil {
		return ""
	}
	if hostAppSpec.CgroupPath == nil {
		cgroupBaseDir := ""
		switch hostAppSpec.QoS {
		case ext.QoSLSE, ext.QoSLSR, ext.QoSLS:
			cgroupBaseDir = hostLSCgroupDir
		case ext.QoSBE:
			cgroupBaseDir = hostBECgroupDir
			// empty string for QoSNone as default
		}
		return filepath.Join(cgroupBaseDir, hostAppSpec.Name)
	} else {
		cgroupBaseDir := ""
		switch hostAppSpec.CgroupPath.Base {
		case slov1alpha1.CgroupBaseTypeKubepods:
			cgroupBaseDir = util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
		case slov1alpha1.CgroupBaseTypeKubeBurstable:
			cgroupBaseDir = util.GetPodQoSRelativePath(corev1.PodQOSBurstable)
		case slov1alpha1.CgroupBaseTypeKubeBesteffort:
			cgroupBaseDir = util.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
			// empty string for CgroupBaseTypeRoot as default
		}
		return filepath.Join(cgroupBaseDir, hostAppSpec.CgroupPath.ParentDir, hostAppSpec.CgroupPath.RelativePath)
	}
}

type HostAppRequest struct {
	Name         string
	QOSClass     ext.QoSClass
	CgroupParent string
}

func (r *HostAppRequest) FromReconciler(hostAppSpec *slov1alpha1.HostApplicationSpec) {
	r.Name = hostAppSpec.Name
	r.QOSClass = hostAppSpec.QoS
	r.CgroupParent = getHostCgroupRelativePath(hostAppSpec)
}

type HostAppResponse struct {
	Resources Resources
}

type HostAppContext struct {
	Request  HostAppRequest
	Response HostAppResponse
	executor resourceexecutor.ResourceUpdateExecutor
}

func (c *HostAppContext) FromReconciler(hostAppSpec *slov1alpha1.HostApplicationSpec) {
	c.Request.FromReconciler(hostAppSpec)
}

func (c *HostAppContext) ReconcilerDone(executor resourceexecutor.ResourceUpdateExecutor) {
	if c.executor == nil {
		c.executor = executor
	}
	c.injectForOrigin()
	c.injectForExt()
}

func (c *HostAppContext) injectForOrigin() {
	// If CPUSet is not nil and is not an empty string, set cpuset
	if c.Response.Resources.CPUSet != nil && *c.Response.Resources.CPUSet != "" {
		eventHelper := audit.V(3).Group(c.Request.Name).Reason("runtime-hooks").Message(
			"set host application cpuset to %v", *c.Response.Resources.CPUSet)
		err := injectCPUSet(c.Request.CgroupParent, *c.Response.Resources.CPUSet, eventHelper, c.executor)
		if err != nil && resourceexecutor.IsCgroupDirErr(err) {
			klog.V(5).Infof("set host application %v cpuset %v on cgroup parent %v failed, error %v",
				c.Request.Name, *c.Response.Resources.CPUSet, c.Request.CgroupParent, err)
		} else if err != nil {
			klog.Infof("set host application %v cpuset %v on cgroup parent %v failed, error %v",
				c.Request.Name, *c.Response.Resources.CPUSet, c.Request.CgroupParent, err)
		} else {
			klog.V(5).Infof("set host application %v cpuset %v on cgroup parent %v",
				c.Request.Name, *c.Response.Resources.CPUSet, c.Request.CgroupParent)
		}
	}
}

func (c *HostAppContext) injectForExt() {
	if c.Response.Resources.CPUBvt != nil {
		eventHelper := audit.V(3).Group(c.Request.Name).Reason("runtime-hooks").Message(
			"set host application bvt to %v", *c.Response.Resources.CPUBvt)
		if err := injectCPUBvt(c.Request.CgroupParent, *c.Response.Resources.CPUBvt, eventHelper, c.executor); err != nil {
			klog.Infof("set host application %v bvt %v on cgroup parent %v failed, error %v", c.Request.Name,
				*c.Response.Resources.CPUBvt, c.Request.CgroupParent, err)
		} else {
			klog.V(5).Infof("set host application %v bvt %v on cgroup parent %v", c.Request.Name,
				*c.Response.Resources.CPUBvt, c.Request.CgroupParent)
		}
	}
}
