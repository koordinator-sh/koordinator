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

package extension

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AnnotationDeviceAllocateHint = SchedulingDomainPrefix + "/device-allocate-hint"
)

type DeviceAllocateHint struct {
	RDMA     *RDMAAllocateHint     `json:"rdma,omitempty"`
	NVSwitch *NVSwitchAllocateHint `json:"nvswitch,omitempty"`
}

type RDMAAllocateHint struct {
	DesiredDeviceType string                `json:"desiredDeviceType,omitempty"`
	AllocateStrategy  RDMAAllocateStrategy  `json:"allocateStrategy,omitempty"`
	LabelSelector     *metav1.LabelSelector `json:"selector,omitempty"`
}

type NVSwitchAllocateHint struct {
	AllocateStrategy NVSwitchAllocateStrategy `json:"allocateStrategy,omitempty"`
}

type RDMAAllocateStrategy string

const (
	RDMAAllocateStrategyAffinityGPUByNUMATopology RDMAAllocateStrategy = "AffinityGPUByNUMATopology"
)

type NVSwitchAllocateStrategy string

const (
	NVSwitchAllocateStrategyApplyForAll NVSwitchAllocateStrategy = "ApplyForAll"
	NVSwitchAllocateStrategyApplyByGPU  NVSwitchAllocateStrategy = "ApplyByGPU"
)

func SetDeviceAllocateHint(obj metav1.Object, hint *DeviceAllocateHint) error {
	data, err := json.Marshal(hint)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationDeviceAllocateHint] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

func GetDeviceAllocateHint(annotations map[string]string) (*DeviceAllocateHint, error) {
	var hint DeviceAllocateHint
	if val, ok := annotations[AnnotationDeviceAllocateHint]; ok {
		err := json.Unmarshal([]byte(val), &hint)
		if err != nil {
			return nil, err
		}
	}
	return &hint, nil
}
