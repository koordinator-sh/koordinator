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

package deviceshare

import (
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

// deviceResources is used to present resources per device.
// we use the minor of device as key
// "0": {koordinator.sh/gpu-core:100, koordinator.sh/gpu-memory-ratio:100, koordinator.sh/gpu-memory: 16GB}
// "1": {koordinator.sh/gpu-core:100, koordinator.sh/gpu-memory-ratio:100, koordinator.sh/gpu-memory: 16GB}
type deviceResources map[int]corev1.ResourceList

func (r deviceResources) DeepCopy() deviceResources {
	if r == nil {
		return nil
	}
	out := deviceResources{}
	for k, v := range r {
		out[k] = v.DeepCopy()
	}
	return out
}

func (r deviceResources) append(in deviceResources) {
	if r == nil {
		return
	}
	for minor, resources := range in {
		device, ok := r[minor]
		if !ok {
			r[minor] = resources.DeepCopy()
		} else {
			util.AddResourceList(device, resources)
			r[minor] = device
		}
	}
}

func (r deviceResources) subtract(in deviceResources) {
	if len(r) == 0 {
		return
	}
	for minor, res := range in {
		resourceList, ok := r[minor]
		if !ok {
			continue
		}
		resourceNames := quotav1.ResourceNames(resourceList)
		resourceList = quotav1.SubtractWithNonNegativeResult(resourceList, quotav1.Mask(res, resourceNames))
		if quotav1.IsZero(resourceList) {
			delete(r, minor)
		} else {
			r[minor] = resourceList
		}
	}
}

func copyDeviceResources(m map[schedulingv1alpha1.DeviceType]deviceResources) map[schedulingv1alpha1.DeviceType]deviceResources {
	r := map[schedulingv1alpha1.DeviceType]deviceResources{}
	for k, v := range m {
		r[k] = v.DeepCopy()
	}
	return r
}

func subtractAllocated(m, podAllocated map[schedulingv1alpha1.DeviceType]deviceResources) map[schedulingv1alpha1.DeviceType]deviceResources {
	for deviceType, devicesResources := range podAllocated {
		ra := m[deviceType]
		if len(ra) == 0 {
			continue
		}
		ra.subtract(devicesResources)
		if len(ra) == 0 {
			delete(m, deviceType)
		}
	}
	return m
}

func appendAllocated(m map[schedulingv1alpha1.DeviceType]deviceResources, podAllocatedList ...map[schedulingv1alpha1.DeviceType]deviceResources) map[schedulingv1alpha1.DeviceType]deviceResources {
	if m == nil {
		m = map[schedulingv1alpha1.DeviceType]deviceResources{}
	}
	for _, allocated := range podAllocatedList {
		for deviceType, deviceResources := range allocated {
			devices := m[deviceType]
			if devices == nil {
				m[deviceType] = deviceResources.DeepCopy()
			} else {
				devices.append(deviceResources)
			}
		}
	}

	return m
}

func appendAllocatedIntersectionDeviceType(m, podAllocated map[schedulingv1alpha1.DeviceType]deviceResources) map[schedulingv1alpha1.DeviceType]deviceResources {
	if m == nil {
		m = map[schedulingv1alpha1.DeviceType]deviceResources{}
	}
	for deviceType, deviceResources := range podAllocated {
		devices := m[deviceType]
		devices.append(deviceResources)
	}
	return m
}

func newDeviceMinorMap(m map[schedulingv1alpha1.DeviceType]deviceResources) map[schedulingv1alpha1.DeviceType]sets.Int {
	r := map[schedulingv1alpha1.DeviceType]sets.Int{}
	for deviceType, resources := range m {
		r[deviceType] = sets.IntKeySet(resources)
	}
	return r
}

type deviceResourceMinorPair struct {
	preferred bool
	minor     int
	resources corev1.ResourceList
}

func sortDeviceResourcesByMinor(resources deviceResources, preferred sets.Int) []deviceResourceMinorPair {
	r := make([]deviceResourceMinorPair, 0, len(resources))
	for k, v := range resources {
		r = append(r, deviceResourceMinorPair{
			preferred: preferred.Has(k),
			minor:     k,
			resources: v,
		})
	}
	sort.Slice(r, func(i, j int) bool {
		if r[i].preferred && !r[j].preferred {
			return true
		} else if !r[i].preferred && r[j].preferred {
			return false
		}
		return r[i].minor < r[j].minor
	})
	return r
}
