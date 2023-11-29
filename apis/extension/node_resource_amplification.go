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
	"fmt"
	"math"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// AnnotationNodeResourceAmplificationRatio denotes the resource amplification ratio of the node.
	AnnotationNodeResourceAmplificationRatio = NodeDomainPrefix + "/resource-amplification-ratio"

	// AnnotationNodeRawAllocatable denotes the un-amplified raw allocatable of the node.
	AnnotationNodeRawAllocatable = NodeDomainPrefix + "/raw-allocatable"
)

// Ratio is a float64 wrapper which will always be json marshalled with precision 2.
type Ratio float64

func (f Ratio) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(float64(f), 'f', 2, 64)), nil
}

// GetNodeResourceAmplificationRatios gets the resource amplification ratios of node from annotations.
func GetNodeResourceAmplificationRatios(annotations map[string]string) (map[corev1.ResourceName]Ratio, error) {
	s, ok := annotations[AnnotationNodeResourceAmplificationRatio]
	if !ok {
		return nil, nil
	}

	var ratios map[corev1.ResourceName]Ratio
	if err := json.Unmarshal([]byte(s), &ratios); err != nil {
		return nil, fmt.Errorf("failed to unmarshal node resource amplification ratio: %w", err)
	}

	return ratios, nil
}

// GetNodeResourceAmplificationRatio gets the amplification ratio of a specific resource of node from annotations.
// It returns -1 without an error when the amplification ratio is not set for this resource.
func GetNodeResourceAmplificationRatio(annotations map[string]string, resource corev1.ResourceName) (Ratio, error) {
	ratios, err := GetNodeResourceAmplificationRatios(annotations)
	if err != nil {
		return -1, err
	}

	ratio, ok := ratios[resource]
	if !ok {
		return -1, nil
	}

	return ratio, nil
}

// SetNodeResourceAmplificationRatios sets the node annotation according to the resource amplification ratios.
// NOTE: The ratio will be converted to string with the precision 2. e.g. 3.1415926 -> 3.14.
func SetNodeResourceAmplificationRatios(node *corev1.Node, ratios map[corev1.ResourceName]Ratio) {
	s, _ := json.Marshal(ratios)
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[AnnotationNodeResourceAmplificationRatio] = string(s)
}

// SetNodeResourceAmplificationRatio sets the amplification ratio of a specific resource of the node.
// It returns true if the ratio changes.
// NOTE: The ratio will be converted to string with the precision 2. e.g. 3.1415926 -> 3.14.
func SetNodeResourceAmplificationRatio(node *corev1.Node, resource corev1.ResourceName, ratio Ratio) (bool, error) {
	ratios, err := GetNodeResourceAmplificationRatios(node.Annotations)
	if err != nil {
		return false, err
	}

	if old := ratios[resource]; old == ratio {
		return false, nil
	}

	if ratios == nil {
		ratios = map[corev1.ResourceName]Ratio{}
	}
	ratios[resource] = ratio
	SetNodeResourceAmplificationRatios(node, ratios)
	return true, nil
}

// HasNodeRawAllocatable checks if the node has raw allocatable annotation.
func HasNodeRawAllocatable(annotations map[string]string) bool {
	_, ok := annotations[AnnotationNodeRawAllocatable]
	return ok
}

// GetNodeRawAllocatable gets the raw allocatable of node from annotations.
func GetNodeRawAllocatable(annotations map[string]string) (corev1.ResourceList, error) {
	s, ok := annotations[AnnotationNodeRawAllocatable]
	if !ok {
		return nil, nil
	}

	var allocatable corev1.ResourceList
	if err := json.Unmarshal([]byte(s), &allocatable); err != nil {
		return nil, fmt.Errorf("failed to unmarshal node raw allocatable: %w", err)
	}

	return allocatable, nil
}

// SetNodeRawAllocatable sets the node annotation according to the raw allocatable.
func SetNodeRawAllocatable(node *corev1.Node, allocatable corev1.ResourceList) {
	s, _ := json.Marshal(allocatable)
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[AnnotationNodeRawAllocatable] = string(s)
}

func AmplifyResourceList(requests corev1.ResourceList, amplificationRatios map[corev1.ResourceName]Ratio, resourceNames ...corev1.ResourceName) {
	fn := func(resourceName corev1.ResourceName) {
		ratio := amplificationRatios[resourceName]
		if ratio <= 1 {
			return
		}
		quantity := requests[resourceName]
		if quantity.IsZero() {
			return
		}

		if resourceName == corev1.ResourceCPU {
			cpu := Amplify(quantity.MilliValue(), ratio)
			requests[resourceName] = *resource.NewMilliQuantity(cpu, resource.DecimalSI)
		} else if resourceName == corev1.ResourceMemory || resourceName == corev1.ResourceEphemeralStorage {
			val := Amplify(quantity.Value(), ratio)
			requests[resourceName] = *resource.NewQuantity(val, resource.BinarySI)
		} else {
			val := Amplify(quantity.Value(), ratio)
			requests[resourceName] = *resource.NewQuantity(val, resource.DecimalSI)
		}
	}

	if len(resourceNames) > 0 {
		for _, name := range resourceNames {
			fn(name)
		}
	} else {
		for name := range requests {
			fn(name)
		}
	}
}

func Amplify(origin int64, ratio Ratio) int64 {
	if ratio <= 1 {
		return origin
	}
	return int64(math.Ceil(float64(origin) * float64(ratio)))
}
