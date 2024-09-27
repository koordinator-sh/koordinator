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

	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

type KubeQOSRequet struct {
	KubeQOSClass corev1.PodQOSClass
	CgroupParent string
}

func (r *KubeQOSRequet) FromReconciler(kubeQOS corev1.PodQOSClass) {
	r.KubeQOSClass = kubeQOS
	r.CgroupParent = util.GetPodQoSRelativePath(kubeQOS)
}

type KubeQOSResponse struct {
	Resources Resources
}

type KubeQOSContext struct {
	Request  KubeQOSRequet
	Response KubeQOSResponse
	executor resourceexecutor.ResourceUpdateExecutor
	updaters []resourceexecutor.ResourceUpdater
}

func (k *KubeQOSContext) RecordEvent(r record.EventRecorder, pod *corev1.Pod) {
	//TODO: Don't record pods by QoS
}

func (k *KubeQOSContext) FromReconciler(kubeQOS corev1.PodQOSClass) {
	k.Request.FromReconciler(kubeQOS)
}

// ReconcilerProcess generate the resource updaters but not do the update until the Update() is called.
func (k *KubeQOSContext) ReconcilerProcess(executor resourceexecutor.ResourceUpdateExecutor) {
	if k.executor == nil {
		k.executor = executor
	}
	k.injectForOrigin()
	k.injectForExt()
}

func (k *KubeQOSContext) ReconcilerDone(executor resourceexecutor.ResourceUpdateExecutor) {
	k.ReconcilerProcess(executor)
	k.Update()
}

func (k *KubeQOSContext) GetUpdaters() []resourceexecutor.ResourceUpdater {
	return k.updaters
}

func (k *KubeQOSContext) Update() {
	k.executor.UpdateBatch(true, k.updaters...)
	k.updaters = nil
}

func (k *KubeQOSContext) injectForOrigin() {
	// TODO
}

func (k *KubeQOSContext) injectForExt() {
	if k.Response.Resources.CPUBvt != nil {
		eventHelper := audit.V(3).Group(string(k.Request.KubeQOSClass)).Reason("runtime-hooks").Message(
			"set kubeqos bvt to %v", *k.Response.Resources.CPUBvt)
		updater, err := injectCPUBvt(k.Request.CgroupParent, *k.Response.Resources.CPUBvt, eventHelper, k.executor)
		if err != nil {
			klog.Infof("set kubeqos %v bvt %v on cgroup parent %v failed, error %v", k.Request.KubeQOSClass,
				*k.Response.Resources.CPUBvt, k.Request.CgroupParent, err)
		} else {
			k.updaters = append(k.updaters, updater)
			klog.V(5).Infof("set kubeqos %v bvt %v on cgroup parent %v", k.Request.KubeQOSClass,
				*k.Response.Resources.CPUBvt, k.Request.CgroupParent)
		}
	}
	if k.Response.Resources.CPUIdle != nil {
		eventHelper := audit.V(3).Group(string(k.Request.KubeQOSClass)).Reason("runtime-hooks").Message(
			"set kubeqos idle to %v", *k.Response.Resources.CPUIdle)
		updater, err := injectCPUIdle(k.Request.CgroupParent, *k.Response.Resources.CPUIdle, eventHelper, k.executor)
		if err != nil {
			klog.Infof("set kubeqos %v idle %v on cgroup parent %v failed, error %v", k.Request.KubeQOSClass,
				*k.Response.Resources.CPUIdle, k.Request.CgroupParent, err)
		} else {
			k.updaters = append(k.updaters, updater)
			klog.V(5).Infof("set kubeqos %v idle %v on cgroup parent %v", k.Request.KubeQOSClass,
				*k.Response.Resources.CPUIdle, k.Request.CgroupParent)
		}
	}
}
