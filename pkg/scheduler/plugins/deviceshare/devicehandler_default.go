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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func init() {
	deviceHandlers[schedulingv1alpha1.RDMA] = &DefaultDeviceHandler{deviceType: schedulingv1alpha1.RDMA, resourceName: apiext.ResourceRDMA}
	deviceHandlers[schedulingv1alpha1.FPGA] = &DefaultDeviceHandler{deviceType: schedulingv1alpha1.FPGA, resourceName: apiext.ResourceFPGA}
}

var _ DeviceHandler = &DefaultDeviceHandler{}

type DefaultDeviceHandler struct {
	deviceType   schedulingv1alpha1.DeviceType
	resourceName corev1.ResourceName
}

func (h *DefaultDeviceHandler) CalcDesiredRequestsAndCount(node *corev1.Node, pod *corev1.Pod, podRequests corev1.ResourceList, nodeDevice *nodeDevice, hint *apiext.DeviceHint, state *preFilterState) (corev1.ResourceList, int, *framework.Status) {
	totalDevices := nodeDevice.deviceTotal[h.deviceType]
	if len(totalDevices) == 0 {
		return nil, 0, framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("Insufficient %s devices", h.deviceType))
	}

	requests := podRequests
	desiredCount := int64(1)

	quantity := podRequests[h.resourceName]
	multiDevices := quantity.Value() > 100 && quantity.Value()%100 == 0
	if multiDevices {
		desiredCount = quantity.Value() / 100
		requests = corev1.ResourceList{
			h.resourceName: *resource.NewQuantity(quantity.Value()/desiredCount, resource.DecimalSI),
		}
	} else if hint != nil {
		switch hint.AllocateStrategy {
		case apiext.ApplyForAllDeviceAllocateStrategy:
			desiredCount = int64(len(totalDevices))
			if hint.Selector != nil {
				selector, err := util.GetFastLabelSelector(hint.Selector)
				if err != nil {
					return nil, 0, framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("invalid Selector of DeviceHint, deviceType: %s, err: %s", h.deviceType, err.Error()))
				}

				matched := 0
				for _, v := range nodeDevice.deviceInfos[h.deviceType] {
					if selector.Matches(labels.Set(v.Labels)) {
						matched++
					}
				}
				desiredCount = int64(matched)
			}
			if desiredCount == 0 {
				return nil, 0, framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("Insufficient %s devices", h.deviceType))
			}
		case apiext.RequestsAsCountAllocateStrategy:
			desiredCount = quantity.Value()
			desiredQuantity := 1
			if hint.ExclusivePolicy == apiext.DeviceLevelDeviceExclusivePolicy {
				desiredQuantity = 100
			}
			requests = corev1.ResourceList{
				h.resourceName: *resource.NewQuantity(int64(desiredQuantity), resource.DecimalSI),
			}
		}
	}
	return requests, int(desiredCount), nil
}
