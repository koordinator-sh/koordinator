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

package podcgroup

import (
	"encoding/json"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/api/v1/resource"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/cgroup"
)

const (
	ResourceEphemeralIops v1.ResourceName = "kubernetes.io/ephemeral-iops"
	ResourceEphemeralBps  v1.ResourceName = "kubernetes.io/ephemeral-bps"
)

type PodResource struct {
	cgroup.Resource
	Pod     *v1.Pod `json:"-"`
	Request int64   `json:"request"`
	Limit   int64   `json:"limit"`
}

func (r *PodResource) String() string {
	return fmt.Sprintf("%s: %s (%s/%s) (%.2f%%/%.2f%%) ~ P(%s)",
		klog.KObj(r.Pod), r.Current(), r.Promise(), r.Throttle(),
		float64(r.Current().Int64())/float64(r.Promise().Int64())*100,
		float64(r.Current().Int64())/float64(r.Throttle().Int64())*100,
		r.Pressure())
}

type PodCgroup struct {
	*cgroup.Cgroup
	Pod *v1.Pod
}

type ResourceGetter interface {
	Get(*PodCgroup) *PodResource
	Name() string
}

var (
	Cpu    ResourceGetter = cpuGetter{}
	Memory ResourceGetter = memoryGetter{}
	Bps    ResourceGetter = bpsGetter{}
	Iops   ResourceGetter = iopsGetter{}
)

type cpuGetter struct{}
type memoryGetter struct{}
type bpsGetter struct{}
type iopsGetter struct{}

func (cpuGetter) Name() string {
	return "Cpu"
}

func (c cpuGetter) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.Name() + "Getter")
}

func (memoryGetter) Name() string {
	return "Memory"
}

func (m memoryGetter) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.Name() + "Getter")
}

func (bpsGetter) Name() string {
	return "Bps"
}

func (b bpsGetter) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.Name() + "Getter")
}

func (iopsGetter) Name() string {
	return "Iops"
}

func (i iopsGetter) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Name() + "Getter")
}

func (cpuGetter) Get(pc *PodCgroup) *PodResource {
	requests := resource.PodRequests(pc.Pod, resource.PodResourcesOptions{})
	limits := resource.PodLimits(pc.Pod, resource.PodResourcesOptions{})
	res := &PodResource{
		Pod:      pc.Pod,
		Resource: pc.Cgroup.Cpu,
	}
	if cpu, ok := requests[v1.ResourceCPU]; ok {
		res.Request = cpu.MilliValue() * time.Second.Microseconds() / 1000
	}
	if cpu, ok := limits[v1.ResourceCPU]; ok {
		res.Limit = cpu.MilliValue() * time.Second.Microseconds() / 1000
	}
	return res
}

func (memoryGetter) Get(pc *PodCgroup) *PodResource {
	requests := resource.PodRequests(pc.Pod, resource.PodResourcesOptions{})
	limits := resource.PodLimits(pc.Pod, resource.PodResourcesOptions{})
	res := &PodResource{
		Pod:      pc.Pod,
		Resource: pc.Cgroup.Memory,
	}
	if mem, ok := requests[v1.ResourceMemory]; ok {
		res.Request = mem.Value()
	}
	if mem, ok := limits[v1.ResourceMemory]; ok {
		res.Limit = mem.Value()
	}
	return res
}

func (bpsGetter) Get(pc *PodCgroup) *PodResource {
	requests := resource.PodRequests(pc.Pod, resource.PodResourcesOptions{})
	limits := resource.PodLimits(pc.Pod, resource.PodResourcesOptions{})
	res := &PodResource{
		Pod:      pc.Pod,
		Resource: pc.Cgroup.Bps,
	}
	if bps, ok := requests[ResourceEphemeralBps]; ok {
		res.Request = bps.Value()
	}
	if bps, ok := limits[ResourceEphemeralBps]; ok {
		res.Limit = bps.Value()
	}
	return res
}

func (iopsGetter) Get(pc *PodCgroup) *PodResource {
	requests := resource.PodRequests(pc.Pod, resource.PodResourcesOptions{})
	limits := resource.PodLimits(pc.Pod, resource.PodResourcesOptions{})
	res := &PodResource{
		Pod:      pc.Pod,
		Resource: pc.Cgroup.Iops,
	}
	if iops, ok := requests[ResourceEphemeralIops]; ok {
		res.Request = iops.Value()
	}
	if iops, ok := limits[ResourceEphemeralIops]; ok {
		res.Limit = iops.Value()
	}
	return res
}
