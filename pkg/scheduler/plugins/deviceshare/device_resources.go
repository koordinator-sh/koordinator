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

func (r deviceResources) append(in deviceResources, hintMinors sets.Int) {
	if r == nil {
		return
	}
	for minor, resources := range in {
		if hintMinors.Len() > 0 && !hintMinors.Has(minor) {
			continue
		}
		device, ok := r[minor]
		if !ok {
			r[minor] = resources.DeepCopy()
		} else {
			util.AddResourceList(device, resources)
			r[minor] = device
		}
	}
}

func (r deviceResources) subtract(in deviceResources, withNonNegativeResult bool) {
	if r == nil {
		return
	}
	for minor, res := range in {
		resourceList := r[minor]
		if withNonNegativeResult {
			resourceList = quotav1.SubtractWithNonNegativeResult(resourceList, res)
		} else {
			resourceList = quotav1.Subtract(resourceList, res)
		}
		if quotav1.IsZero(resourceList) {
			delete(r, minor)
		} else {
			r[minor] = resourceList
		}
	}
}

func (r deviceResources) isZero() bool {
	for _, v := range r {
		if !quotav1.IsZero(v) {
			return false
		}
	}
	return true
}

func copyDeviceResources(m map[schedulingv1alpha1.DeviceType]deviceResources) map[schedulingv1alpha1.DeviceType]deviceResources {
	r := map[schedulingv1alpha1.DeviceType]deviceResources{}
	for k, v := range m {
		r[k] = v.DeepCopy()
	}
	return r
}

func subtractAllocated(m, allocated map[schedulingv1alpha1.DeviceType]deviceResources, withNonNegativeResult bool) map[schedulingv1alpha1.DeviceType]deviceResources {
	for deviceType, devicesResources := range allocated {
		ra := m[deviceType]
		if ra == nil {
			ra = deviceResources{}
			m[deviceType] = ra
		}
		ra.subtract(devicesResources, withNonNegativeResult)
		if len(ra) == 0 {
			delete(m, deviceType)
		}
	}
	return m
}

func appendAllocated(m map[schedulingv1alpha1.DeviceType]deviceResources, allocatedList ...map[schedulingv1alpha1.DeviceType]deviceResources) map[schedulingv1alpha1.DeviceType]deviceResources {
	if m == nil {
		m = map[schedulingv1alpha1.DeviceType]deviceResources{}
	}
	for _, allocated := range allocatedList {
		for deviceType, deviceResources := range allocated {
			devices := m[deviceType]
			if devices == nil {
				m[deviceType] = deviceResources.DeepCopy()
			} else {
				devices.append(deviceResources, nil)
			}
		}
	}

	return m
}

func appendAllocatedByHints(hints map[schedulingv1alpha1.DeviceType]sets.Int, m map[schedulingv1alpha1.DeviceType]deviceResources, allocatedList ...map[schedulingv1alpha1.DeviceType]deviceResources) map[schedulingv1alpha1.DeviceType]deviceResources {
	if m == nil {
		m = map[schedulingv1alpha1.DeviceType]deviceResources{}
	}
	for hintDeviceType, hintMinors := range hints {
		for _, allocated := range allocatedList {
			for deviceType, resources := range allocated {
				if deviceType != hintDeviceType {
					continue
				}
				devices := m[deviceType]
				if devices == nil {
					devices = deviceResources{}
					m[deviceType] = devices
				}
				devices.append(resources, hintMinors)
			}
		}
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
	score     int64
}

func scoreDevices(podRequest corev1.ResourceList, totalResources, freeResources deviceResources, allocationScorer *resourceAllocationScorer) []deviceResourceMinorPair {
	r := make([]deviceResourceMinorPair, 0, len(freeResources))
	for minor, free := range freeResources {
		var score int64
		if allocationScorer != nil {
			score = allocationScorer.scoreDevice(podRequest, totalResources[minor], free)
		}
		r = append(r, deviceResourceMinorPair{
			minor:     minor,
			resources: free,
			score:     score,
		})
	}
	return r
}

func sortDeviceResourcesByMinor(r []deviceResourceMinorPair, preferred sets.Int) []deviceResourceMinorPair {
	if preferred.Len() > 0 {
		for i := range r {
			r[i].preferred = preferred.Has(r[i].minor)
		}
	}

	sort.Slice(r, func(i, j int) bool {
		if r[i].preferred && !r[j].preferred {
			return true
		} else if !r[i].preferred && r[j].preferred {
			return false
		}
		if r[i].score < r[j].score {
			return false
		} else if r[i].score > r[j].score {
			return true
		}
		return r[i].minor < r[j].minor
	})
	return r
}

func sortDeviceResourcesByPreferredPCIe(r []deviceResourceMinorPair, preferredPCIe sets.String, deviceInfos map[int]*schedulingv1alpha1.DeviceInfo) []deviceResourceMinorPair {
	for i := range r {
		r[i].preferred = false
		minor := r[i].minor
		deviceInfo := deviceInfos[minor]
		if deviceInfo != nil && deviceInfo.Topology != nil && preferredPCIe.Has(deviceInfo.Topology.PCIEID) {
			r[i].preferred = true
		}
	}
	return sortDeviceResourcesByMinor(r, nil)
}
