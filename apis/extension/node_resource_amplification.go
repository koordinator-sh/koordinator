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
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

const (
	// AnnotationNodeResourceAmplificationRatio denotes the resource amplification ratio of the node.
	AnnotationNodeResourceAmplificationRatio = NodeDomainPrefix + "/resource-amplification-ratio"

	// AnnotationNodeRawCapacity denotes the un-amplified raw capacity of the node.
	AnnotationNodeRawCapacity = NodeDomainPrefix + "/raw-capacity"

	// AnnotationNodeRawAllocatable denotes the un-amplified raw allocatable of the node.
	AnnotationNodeRawAllocatable = NodeDomainPrefix + "/raw-allocatable"
)

// Ratio is a float64 wrapper which will always be json marshalled with precision 2.
type Ratio float64

func (f Ratio) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(float64(f), 'f', 2, 64)), nil
}

// GetNodeResourceAmplificationRatios gets the resource amplification ratios of the node.
func GetNodeResourceAmplificationRatios(node *corev1.Node) (map[corev1.ResourceName]Ratio, error) {
	s, ok := node.Annotations[AnnotationNodeResourceAmplificationRatio]
	if !ok {
		return nil, nil
	}

	var ratios map[corev1.ResourceName]Ratio
	if err := json.Unmarshal([]byte(s), &ratios); err != nil {
		return nil, fmt.Errorf("failed to unmarshal node resource amplification ratio: %w", err)
	}

	return ratios, nil
}

// GetNodeResourceAmplificationRatio gets the amplification ratio of a specific resource of the node.
// It returns -1 without an error when the amplification ratio is not set for this resource.
func GetNodeResourceAmplificationRatio(node *corev1.Node, resource corev1.ResourceName) (Ratio, error) {
	ratios, err := GetNodeResourceAmplificationRatios(node)
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
	ratios, err := GetNodeResourceAmplificationRatios(node)
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

// GetNodeRawCapacity gets the raw capacity of the node.
func GetNodeRawCapacity(node *corev1.Node) (corev1.ResourceList, error) {
	s, ok := node.Annotations[AnnotationNodeRawCapacity]
	if !ok {
		return nil, nil
	}

	var capacity corev1.ResourceList
	if err := json.Unmarshal([]byte(s), &capacity); err != nil {
		return nil, fmt.Errorf("failed to unmarshal node raw capacity: %w", err)
	}

	return capacity, nil
}

// SetNodeRawCapacity sets the node annotation according to the raw capacity.
func SetNodeRawCapacity(node *corev1.Node, capacity corev1.ResourceList) {
	s, _ := json.Marshal(capacity)
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[AnnotationNodeRawCapacity] = string(s)
}

// GetNodeRawAllocatable gets the raw allocatable of the node.
func GetNodeRawAllocatable(node *corev1.Node) (corev1.ResourceList, error) {
	s, ok := node.Annotations[AnnotationNodeRawAllocatable]
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
