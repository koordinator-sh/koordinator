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
	"strings"

	"github.com/containerd/nri/pkg/api"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type ContainerMeta struct {
	Name string
	ID   string // docker://xxx; containerd://
	// is sandbox container
	Sandbox bool
}

func (c *ContainerMeta) FromNri(container *api.Container, podAnnotations map[string]string) {
	c.Name = container.GetName()
	uid := container.GetId()
	c.ID = getContainerID(podAnnotations, uid)
}

func (c *ContainerMeta) FromProxy(containerMeta *runtimeapi.ContainerMetadata, podAnnotations map[string]string) {
	c.Name = containerMeta.GetName()
	uid := containerMeta.GetId()
	c.ID = getContainerID(podAnnotations, uid)
}

type ContainerRequest struct {
	PodMeta           PodMeta
	ContainerMeta     ContainerMeta
	PodLabels         map[string]string
	PodAnnotations    map[string]string
	CgroupParent      string
	ContainerEnvs     map[string]string
	Resources         *Resources // TODO: support proxy & nri mode
	ExtendedResources *apiext.ExtendedResourceContainerSpec
}

func splitEnvVar(s string) (string, string) {
	split := strings.SplitN(s, "=", 2)
	if len(split) < 1 {
		return "", ""
	}
	if len(split) != 2 {
		return split[0], ""
	}
	return split[0], split[1]
}

func (c *ContainerRequest) FromNri(pod *api.PodSandbox, container *api.Container) {
	c.PodMeta.FromNri(pod)
	c.ContainerMeta.FromNri(container, pod.GetAnnotations())
	c.PodLabels = pod.GetLabels()
	c.PodAnnotations = pod.GetAnnotations()
	c.CgroupParent, _ = koordletutil.GetContainerCgroupParentDirByID(pod.Linux.CgroupParent, c.ContainerMeta.ID)

	envs := make(map[string]string)
	for _, e := range container.GetEnv() {
		k, v := splitEnvVar(e)
		if k != "" && v != "" {
			envs[k] = v
		}
	}
	c.ContainerEnvs = envs

	spec, err := apiext.GetExtendedResourceSpec(pod.GetAnnotations())
	if err != nil {
		klog.V(4).Infof("failed to get ExtendedResourceSpec from nri via annotation, container %s/%s, name: %s, err: %s",
			c.PodMeta.Namespace, c.PodMeta.Name, c.ContainerMeta.Name, err)
	}
	if spec != nil && spec.Containers != nil {
		if containerSpec, ok := spec.Containers[c.ContainerMeta.Name]; ok {
			c.ExtendedResources = &containerSpec
		}
	}
}

func (c *ContainerRequest) FromProxy(req *runtimeapi.ContainerResourceHookRequest) {
	c.PodMeta.FromProxy(req.PodMeta)
	c.ContainerMeta.FromProxy(req.ContainerMeta, req.PodAnnotations)
	c.PodLabels = req.GetPodLabels()
	c.PodAnnotations = req.GetPodAnnotations()
	c.CgroupParent, _ = koordletutil.GetContainerCgroupParentDirByID(req.GetPodCgroupParent(), c.ContainerMeta.ID)
	c.ContainerEnvs = req.GetContainerEnvs()
	// retrieve ExtendedResources from pod annotations
	spec, err := apiext.GetExtendedResourceSpec(req.GetPodAnnotations())
	if err != nil {
		klog.V(4).Infof("failed to get ExtendedResourceSpec from proxy via annotation, container %s/%s, name: %s, err: %s",
			c.PodMeta.Namespace, c.PodMeta.Name, c.ContainerMeta.Name, err)
	}
	if spec != nil && spec.Containers != nil {
		if containerSpec, ok := spec.Containers[c.ContainerMeta.Name]; ok {
			c.ExtendedResources = &containerSpec
		}
	}
}

func (c *ContainerRequest) FromReconciler(podMeta *statesinformer.PodMeta, containerName string, sandbox bool) {
	c.PodMeta.FromReconciler(podMeta.Pod.ObjectMeta)
	c.ContainerMeta.Name = containerName
	c.ContainerMeta.Sandbox = sandbox
	if sandbox {
		var err error
		if c.ContainerMeta.ID, err = koordletutil.GetPodSandboxContainerID(podMeta.Pod); err != nil {
			klog.V(4).Infof("failed to get sandbox container ID for pod %s, err: %s",
				podMeta.Key(), err)
			return
		} else if c.ContainerMeta.ID == "" {
			klog.V(4).Infof("container ID is empty for pod %s, pod may not start, skip", podMeta.Key())
			return
		}
	} else {
		for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
			if containerStat.Name == containerName {
				c.ContainerMeta.ID = containerStat.ContainerID
				break
			}
		}
	}
	var specFromContainer *apiext.ExtendedResourceContainerSpec
	for i := range podMeta.Pod.Spec.Containers {
		containerSpec := podMeta.Pod.Spec.Containers[i]
		if containerSpec.Name == containerName {
			if c.ContainerEnvs == nil {
				c.ContainerEnvs = map[string]string{}
			}
			for _, envVar := range containerSpec.Env {
				c.ContainerEnvs[envVar.Name] = envVar.Value
			}
			specFromContainer = util.GetContainerExtendedResources(&containerSpec)
			c.Resources = &Resources{}
			c.Resources.FromContainer(&containerSpec)
			break
		}
	}
	c.PodLabels = podMeta.Pod.Labels
	c.PodAnnotations = podMeta.Pod.Annotations
	c.CgroupParent, _ = koordletutil.GetContainerCgroupParentDirByID(podMeta.CgroupDir, c.ContainerMeta.ID)
	// retrieve ExtendedResources from container spec and pod annotations (prefer container spec)
	specFromAnnotations, err := apiext.GetExtendedResourceSpec(podMeta.Pod.Annotations)
	if err != nil {
		klog.V(4).Infof("failed to get ExtendedResourceSpec from reconciler via annotation, container %s/%s, name: %s, err: %s",
			c.PodMeta.Namespace, c.PodMeta.Name, c.ContainerMeta.Name, err)
	}
	if specFromContainer != nil {
		c.ExtendedResources = specFromContainer
	} else if specFromAnnotations != nil && specFromAnnotations.Containers != nil { // specFromContainer == nil
		if containerSpec, ok := specFromAnnotations.Containers[c.ContainerMeta.Name]; ok {
			c.ExtendedResources = &containerSpec
		}
	}
}

type ContainerResponse struct {
	Resources           Resources
	AddContainerEnvs    map[string]string
	AddContainerDevices []*api.LinuxDevice
}

func (c *ContainerResponse) ProxyDone(resp *runtimeapi.ContainerResourceHookResponse) {
	if c.Resources.IsOriginResSet() && resp.ContainerResources == nil {
		// resource value is injected but origin request is nil, init resource response
		resp.ContainerResources = &runtimeapi.LinuxContainerResources{}
	}
	if c.Resources.CPUSet != nil {
		resp.ContainerResources.CpusetCpus = *c.Resources.CPUSet
	}
	if c.Resources.CFSQuota != nil {
		resp.ContainerResources.CpuQuota = *c.Resources.CFSQuota
	}
	if c.Resources.CPUShares != nil {
		resp.ContainerResources.CpuShares = *c.Resources.CPUShares
	}
	if c.Resources.MemoryLimit != nil {
		resp.ContainerResources.MemoryLimitInBytes = *c.Resources.MemoryLimit
	}
	if c.AddContainerEnvs != nil {
		if resp.ContainerEnvs == nil {
			resp.ContainerEnvs = make(map[string]string)
		}
		for k, v := range c.AddContainerEnvs {
			resp.ContainerEnvs[k] = v
		}
	}
}

type ContainerContext struct {
	Request  ContainerRequest
	Response ContainerResponse
	executor resourceexecutor.ResourceUpdateExecutor
	updaters []resourceexecutor.ResourceUpdater
}

func (c *ContainerContext) RecordEvent(r record.EventRecorder, pod *corev1.Pod) {
	//TODO: Don't record pod by container level
}

func (c *ContainerContext) FromNri(pod *api.PodSandbox, container *api.Container) {
	c.Request.FromNri(pod, container)
}

func (c *ContainerContext) FromProxy(req *runtimeapi.ContainerResourceHookRequest) {
	c.Request.FromProxy(req)
}

func (c *ContainerContext) ProxyDone(resp *runtimeapi.ContainerResourceHookResponse, executor resourceexecutor.ResourceUpdateExecutor) {
	if c.executor == nil {
		c.executor = executor
	}
	c.injectForExt()
	c.Response.ProxyDone(resp)
	c.Update()
}

func (c *ContainerContext) NriDone(executor resourceexecutor.ResourceUpdateExecutor) (*api.ContainerAdjustment, *api.ContainerUpdate, error) {
	if c.executor == nil {
		c.executor = executor
	}
	c.injectForExt()
	adjust := &api.ContainerAdjustment{}
	update := &api.ContainerUpdate{}
	// todo: add more fields conversions
	if c.Response.Resources.CPUSet != nil {
		adjust.SetLinuxCPUSetCPUs(*c.Response.Resources.CPUSet)
		update.SetLinuxCPUSetCPUs(*c.Response.Resources.CPUSet)
	}

	if c.Response.Resources.CFSQuota != nil {
		adjust.SetLinuxCPUQuota(*c.Response.Resources.CFSQuota)
		update.SetLinuxCPUQuota(*c.Response.Resources.CFSQuota)
	}

	if c.Response.Resources.CPUShares != nil {
		adjust.SetLinuxCPUShares(uint64(*c.Response.Resources.CPUShares))
		update.SetLinuxCPUShares(uint64(*c.Response.Resources.CPUShares))
	}

	if c.Response.Resources.MemoryLimit != nil {
		adjust.SetLinuxMemoryLimit(*c.Response.Resources.MemoryLimit)
		update.SetLinuxMemoryLimit(*c.Response.Resources.MemoryLimit)
	}

	if c.Response.Resources.Resctrl != nil {
		adjust.SetLinuxRDTClass((*(c.Response.Resources.Resctrl)).Closid)
		update.SetLinuxRDTClass((*(c.Response.Resources.Resctrl)).Closid)
	}

	if c.Response.AddContainerEnvs != nil {
		for k, v := range c.Response.AddContainerEnvs {
			adjust.AddEnv(k, v)
		}
	}

	if len(c.Response.AddContainerDevices) != 0 {
		for i := range c.Response.AddContainerDevices {
			adjust.AddDevice(c.Response.AddContainerDevices[i])
		}

	}

	c.Update()

	return adjust, update, nil
}

func (c *ContainerContext) FromReconciler(podMeta *statesinformer.PodMeta, containerName string, sandbox bool) {
	c.Request.FromReconciler(podMeta, containerName, sandbox)
}

// ReconcilerProcess generate the resource updaters but not do the update until the Update() is called.
func (c *ContainerContext) ReconcilerProcess(executor resourceexecutor.ResourceUpdateExecutor) {
	if c.executor == nil {
		c.executor = executor
	}
	if len(c.Request.CgroupParent) == 0 {
		klog.V(4).Infof("container cgroup parent is empty, skip reconciler for %v/%v",
			c.Request.PodMeta.String(), c.Request.ContainerMeta.Name)
		return
	}
	c.injectForExt()
	c.injectForOrigin()
}

func (c *ContainerContext) ReconcilerDone(executor resourceexecutor.ResourceUpdateExecutor) {
	c.ReconcilerProcess(executor)
	c.Update()
}

func (c *ContainerContext) GetUpdaters() []resourceexecutor.ResourceUpdater {
	return c.updaters
}

func (c *ContainerContext) Update() {
	c.executor.UpdateBatch(true, c.updaters...)
	c.updaters = nil
}

// Inject valid parameters in ContainerContext.Response.Resources,
// such as CPUShares, CPUSet, CFSQuota, MemoryLimit...
func (c *ContainerContext) injectForOrigin() {
	// If CPUShares is not nil, set container cpu share
	if c.Response.Resources.CPUShares != nil {
		eventHelper := audit.V(3).Container(c.Request.ContainerMeta.ID).Reason("runtime-hooks").Message(
			"set container cpu share to %v", *c.Response.Resources.CPUShares)
		updater, err := injectCPUShares(c.Request.CgroupParent, *c.Response.Resources.CPUShares, eventHelper, c.executor)
		if err != nil {
			klog.Infof("set container %v/%v/%v cpu share %v on cgroup parent %v failed, error %v", c.Request.PodMeta.Namespace,
				c.Request.PodMeta.Name, c.Request.ContainerMeta.Name, *c.Response.Resources.CPUShares, c.Request.CgroupParent, err)
		} else {
			c.updaters = append(c.updaters, updater)
			klog.V(5).Infof("set container %v/%v/%v cpu share %v on cgroup parent %v",
				c.Request.PodMeta.Namespace, c.Request.PodMeta.Name, c.Request.ContainerMeta.Name,
				*c.Response.Resources.CPUShares, c.Request.CgroupParent)
		}
	}
	// If CPUSet is not nil and is not an empty string, set container cpuset
	if c.Response.Resources.CPUSet != nil && *c.Response.Resources.CPUSet != "" {
		eventHelper := audit.V(3).Container(c.Request.ContainerMeta.ID).Reason("runtime-hooks").Message("set container cpuset to %v", *c.Response.Resources.CPUSet)
		updater, err := injectCPUSet(c.Request.CgroupParent, *c.Response.Resources.CPUSet, eventHelper, c.executor)
		if err != nil {
			klog.Infof("set container %v/%v/%v cpuset %v on cgroup parent %v failed, error %v", c.Request.PodMeta.Namespace,
				c.Request.PodMeta.Name, c.Request.ContainerMeta.Name, *c.Response.Resources.CPUSet, c.Request.CgroupParent, err)
		} else {
			c.updaters = append(c.updaters, updater)
			klog.V(5).Infof("set container %v/%v/%v cpuset %v on cgroup parent %v",
				c.Request.PodMeta.Namespace, c.Request.PodMeta.Name, c.Request.ContainerMeta.Name,
				*c.Response.Resources.CPUSet, c.Request.CgroupParent)
		}
	}
	// If CFSQuota is not nil, set container cfs quota
	if c.Response.Resources.CFSQuota != nil {
		eventHelper := audit.V(3).Container(c.Request.ContainerMeta.ID).Reason("runtime-hooks").Message(
			"set container cfs quota to %v", *c.Response.Resources.CFSQuota)
		updater, err := injectCPUQuota(c.Request.CgroupParent, *c.Response.Resources.CFSQuota, eventHelper, c.executor)
		if err != nil {
			klog.Infof("set container %v/%v/%v cfs quota %v on cgroup parent %v failed, error %v", c.Request.PodMeta.Namespace,
				c.Request.PodMeta.Name, c.Request.ContainerMeta.Name, *c.Response.Resources.CFSQuota, c.Request.CgroupParent, err)
		} else {
			c.updaters = append(c.updaters, updater)
			klog.V(5).Infof("set container %v/%v/%v cfs quota %v on cgroup parent %v",
				c.Request.PodMeta.Namespace, c.Request.PodMeta.Name, c.Request.ContainerMeta.Name,
				*c.Response.Resources.CFSQuota, c.Request.CgroupParent)
		}
	}
	// If MemoryLimit is not nil, set container memory limit
	if c.Response.Resources.MemoryLimit != nil {
		eventHelper := audit.V(3).Container(c.Request.ContainerMeta.ID).Reason("runtime-hooks").Message(
			"set container memory limit to %v", *c.Response.Resources.MemoryLimit)
		updater, err := injectMemoryLimit(c.Request.CgroupParent, *c.Response.Resources.MemoryLimit, eventHelper, c.executor)
		if err != nil {
			klog.Infof("set container %v/%v/%v memory limit %v on cgroup parent %v failed, error %v", c.Request.PodMeta.Namespace,
				c.Request.PodMeta.Name, c.Request.ContainerMeta.Name, *c.Response.Resources.MemoryLimit, c.Request.CgroupParent, err)
		} else {
			c.updaters = append(c.updaters, updater)
			klog.V(5).Infof("set container %v/%v/%v memory limit %v on cgroup parent %v",
				c.Request.PodMeta.Namespace, c.Request.PodMeta.Name, c.Request.ContainerMeta.Name,
				*c.Response.Resources.MemoryLimit, c.Request.CgroupParent)
		}
	}
	// TODO other fields
}

func (c *ContainerContext) injectForExt() {
	// TODO
}

func getContainerID(podAnnotations map[string]string, containerUID string) string {
	// TODO parse from runtime hook request directly such as cgroup path format
	runtimeType := system.Conf.DefaultRuntimeType
	if _, exist := podAnnotations["io.kubernetes.docker.type"]; exist {
		runtimeType = system.RuntimeTypeDocker
	}
	return fmt.Sprintf("%s://%s", runtimeType, containerUID)
}
