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
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/api/v1/resource"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

type HooksProtocol interface {
	ReconcilerDone(executor resourceexecutor.ResourceUpdateExecutor)
	Update()
	GetUpdaters() []resourceexecutor.ResourceUpdater
	RecordEvent(r record.EventRecorder, pod *corev1.Pod)
}

type hooksProtocolBuilder struct {
	KubeQOS   func(kubeQOS corev1.PodQOSClass) HooksProtocol
	Pod       func(podMeta *statesinformer.PodMeta) HooksProtocol
	Sandbox   func(podMeta *statesinformer.PodMeta) HooksProtocol
	Container func(podMeta *statesinformer.PodMeta, containerName string) HooksProtocol
	HostApp   func(hostAppSpec *slov1alpha1.HostApplicationSpec) HooksProtocol
}

var HooksProtocolBuilder = hooksProtocolBuilder{
	KubeQOS: func(kubeQOS corev1.PodQOSClass) HooksProtocol {
		k := &KubeQOSContext{}
		k.FromReconciler(kubeQOS)
		return k
	},
	Pod: func(podMeta *statesinformer.PodMeta) HooksProtocol {
		p := &PodContext{}
		p.FromReconciler(podMeta)
		return p
	},
	Sandbox: func(podMeta *statesinformer.PodMeta) HooksProtocol {
		p := &ContainerContext{}
		p.FromReconciler(podMeta, "", true)
		return p
	},
	Container: func(podMeta *statesinformer.PodMeta, containerName string) HooksProtocol {
		c := &ContainerContext{}
		c.FromReconciler(podMeta, containerName, false)
		return c
	},
	HostApp: func(hostAppSpec *slov1alpha1.HostApplicationSpec) HooksProtocol {
		c := &HostAppContext{}
		c.FromReconciler(hostAppSpec)
		return c
	},
}

type Resctrl struct {
	Schemata   string
	Closid     string
	NewTaskIds []int32
}

type Resources struct {
	// origin resources
	CPUShares     *int64
	CFSQuota      *int64
	CPUSet        *string
	MemoryLimit   *int64
	NetClsClassId *uint32

	// extended resources
	CPUBvt  *int64
	CPUIdle *int64
	Resctrl *Resctrl
}

func (r *Resources) IsOriginResSet() bool {
	return r.CPUShares != nil || r.CFSQuota != nil || r.CPUSet != nil || r.MemoryLimit != nil
}

func (r *Resources) FromPod(pod *corev1.Pod) {
	requests := resource.PodRequests(pod, resource.PodResourcesOptions{})
	limits := resource.PodLimits(pod, resource.PodResourcesOptions{})
	cpuShares := sysutil.MilliCPUToShares(requests.Cpu().MilliValue())
	cfsQuota := sysutil.MilliCPUToQuota(limits.Cpu().MilliValue())
	memoryLimit := limits.Memory().Value()
	if memoryLimit <= 0 {
		memoryLimit = -1
	}
	r.CPUShares = &cpuShares
	r.CFSQuota = &cfsQuota
	r.MemoryLimit = &memoryLimit
}

func (r *Resources) FromContainer(container *corev1.Container) {
	if requests := container.Resources.Requests; requests != nil {
		cpuShares := sysutil.MilliCPUToShares(requests.Cpu().MilliValue())
		r.CPUShares = &cpuShares
	} else {
		cpuShares := sysutil.MilliCPUToShares(0)
		r.CPUShares = &cpuShares
	}
	if limits := container.Resources.Limits; limits != nil {
		cfsQuota := sysutil.MilliCPUToQuota(limits.Cpu().MilliValue())
		r.CFSQuota = &cfsQuota
		memoryLimit := limits.Memory().Value()
		if memoryLimit <= 0 {
			memoryLimit = -1
		}
		r.MemoryLimit = &memoryLimit
	} else {
		cfsQuota := sysutil.MilliCPUToQuota(0)
		r.CFSQuota = &cfsQuota
		memoryLimit := int64(-1)
		r.MemoryLimit = &memoryLimit
	}
}

type Mount struct {
	Destination string   `protobuf:"bytes,1,opt,name=destination,proto3" json:"destination,omitempty"`
	Type        string   `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
	Source      string   `protobuf:"bytes,3,opt,name=source,proto3" json:"source,omitempty"`
	Options     []string `protobuf:"bytes,4,rep,name=options,proto3" json:"options,omitempty"`
}

func injectCPUShares(cgroupParent string, cpuShares int64, a *audit.EventHelper, e resourceexecutor.ResourceUpdateExecutor) (resourceexecutor.ResourceUpdater, error) {
	cpuShareStr := strconv.FormatInt(cpuShares, 10)
	updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUSharesName, cgroupParent, cpuShareStr, a)
	if err != nil {
		return nil, err
	}
	return updater, nil
}

func injectCPUSet(cgroupParent string, cpuset string, a *audit.EventHelper, e resourceexecutor.ResourceUpdateExecutor) (resourceexecutor.ResourceUpdater, error) {
	updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUSetCPUSName, cgroupParent, cpuset, a)
	if err != nil {
		return nil, err
	}
	return updater, nil
}

func injectCPUQuota(cgroupParent string, cpuQuota int64, a *audit.EventHelper, e resourceexecutor.ResourceUpdateExecutor) (resourceexecutor.ResourceUpdater, error) {
	cpuQuotaStr := strconv.FormatInt(cpuQuota, 10)
	updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUCFSQuotaName, cgroupParent, cpuQuotaStr, a)
	if err != nil {
		return nil, err
	}
	return updater, nil
}

func injectMemoryLimit(cgroupParent string, memoryLimit int64, a *audit.EventHelper, e resourceexecutor.ResourceUpdateExecutor) (resourceexecutor.ResourceUpdater, error) {
	memoryLimitStr := strconv.FormatInt(memoryLimit, 10)
	updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.MemoryLimitName, cgroupParent, memoryLimitStr, a)
	if err != nil {
		return nil, err
	}
	return updater, nil
}

func injectCPUBvt(cgroupParent string, bvtValue int64, a *audit.EventHelper, e resourceexecutor.ResourceUpdateExecutor) (resourceexecutor.ResourceUpdater, error) {
	bvtValueStr := strconv.FormatInt(bvtValue, 10)
	updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUBVTWarpNsName, cgroupParent, bvtValueStr, a)
	if err != nil {
		return nil, err
	}
	return updater, nil
}

func injectCPUIdle(cgroupParent string, idleValue int64, a *audit.EventHelper, e resourceexecutor.ResourceUpdateExecutor) (resourceexecutor.ResourceUpdater, error) {
	idleValueStr := strconv.FormatInt(idleValue, 10)
	updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUIdleName, cgroupParent, idleValueStr, a)
	if err != nil {
		return nil, err
	}
	return updater, nil
}

func injectNetClsClassId(cgroupParent string, classId uint32, a *audit.EventHelper, e resourceexecutor.ResourceUpdateExecutor) (resourceexecutor.ResourceUpdater, error) {
	clsIdStr := strconv.FormatUint(uint64(classId), 10)
	updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.NetClsClassIdName, cgroupParent, clsIdStr, a)
	if err != nil {
		return nil, err
	}
	return updater, nil
}

func createCatGroup(closid string, a *audit.EventHelper, e resourceexecutor.ResourceUpdateExecutor) (resourceexecutor.ResourceUpdater, error) {
	updater, err := resourceexecutor.NewCatGroupResource(closid, a)
	if err != nil {
		return nil, err
	}
	return updater, nil
}

func injectResctrl(closid string, schemata string, e *audit.EventHelper, executor resourceexecutor.ResourceUpdateExecutor) (resourceexecutor.ResourceUpdater, error) {
	updater, err := resourceexecutor.NewResctrlSchemataResource(closid, schemata, e)
	if err != nil {
		return nil, err
	}
	return updater, nil
}
