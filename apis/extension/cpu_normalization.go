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
	"k8s.io/utils/pointer"
)

const (
	// AnnotationCPUNormalizationRatio denotes the cpu normalization ratio of the node.
	AnnotationCPUNormalizationRatio = NodeDomainPrefix + "/cpu-normalization-ratio"

	// LabelCPUNormalizationEnabled indicates whether the cpu normalization is enabled on the node.
	// If both the label and node-level CPUNormalizationStrategy is set, the label overrides the strategy.
	LabelCPUNormalizationEnabled = NodeDomainPrefix + "/cpu-normalization-enabled"

	// AnnotationCPUBasicInfo denotes the basic CPU info of the node.
	AnnotationCPUBasicInfo = NodeDomainPrefix + "/cpu-basic-info"

	// NormalizationRatioDiffEpsilon is the min difference between two cpu normalization ratios.
	NormalizationRatioDiffEpsilon = 0.01
)

// GetCPUNormalizationRatio gets the cpu normalization ratio from the node.
// It returns -1 without an error when the cpu normalization annotation is missing.
func GetCPUNormalizationRatio(node *corev1.Node) (float64, error) {
	if node.Annotations == nil {
		return -1, nil
	}
	s, ok := node.Annotations[AnnotationCPUNormalizationRatio]
	if !ok {
		return -1, nil
	}

	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return -1, fmt.Errorf("parse cpu normalization ratio failed, err: %w", err)
	}
	if v <= 0 {
		return -1, fmt.Errorf("illegal cpu normalization ratio: %v", v)
	}

	return v, nil
}

// SetCPUNormalizationRatio sets the node annotation according to the cpu-normalization-ratio.
// It returns true if the label value changes.
// NOTE: The ratio will be converted to string with the precision 2. e.g. 3.1415926 -> 3.14.
func SetCPUNormalizationRatio(node *corev1.Node, ratio float64) bool {
	s := strconv.FormatFloat(ratio, 'f', 2, 64)
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	if old := node.Annotations[AnnotationCPUNormalizationRatio]; old == s {
		return false
	}

	node.Annotations[AnnotationCPUNormalizationRatio] = s
	return true
}

func GetCPUNormalizationEnabled(node *corev1.Node) (*bool, error) {
	if node.Labels == nil {
		return nil, nil
	}
	s, ok := node.Labels[LabelCPUNormalizationEnabled]
	if !ok {
		return nil, nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return nil, fmt.Errorf("parse cpu normalization enabled failed, err: %w", err)
	}
	return pointer.Bool(v), nil
}

func IsCPUNormalizationRatioDifferent(old, new float64) bool {
	return old > new+NormalizationRatioDiffEpsilon || old < new-NormalizationRatioDiffEpsilon
}

// CPUBasicInfo describes the cpu basic features and status.
type CPUBasicInfo struct {
	CPUModel           string `json:"cpuModel,omitempty"`
	HyperThreadEnabled bool   `json:"hyperThreadEnabled,omitempty"`
	TurboEnabled       bool   `json:"turboEnabled,omitempty"`
	CatL3CbmMask       string `json:"catL3CbmMask,omitempty"`
	VendorID           string `json:"vendorID,omitempty"`
}

func (c *CPUBasicInfo) Key() string {
	return fmt.Sprintf("%s_%v_%v", c.CPUModel, c.HyperThreadEnabled, c.TurboEnabled)
}

// GetCPUBasicInfo gets the cpu basic info from the node-level annotations.
// It returns nil info without an error when the cpu basic info annotation is missing.
func GetCPUBasicInfo(annotations map[string]string) (*CPUBasicInfo, error) {
	if annotations == nil {
		return nil, nil
	}
	s, ok := annotations[AnnotationCPUBasicInfo]
	if !ok {
		return nil, nil
	}

	var info CPUBasicInfo
	err := json.Unmarshal([]byte(s), &info)
	if err != nil {
		return nil, fmt.Errorf("unmarshal cpu basic info failed, err: %w", err)
	}
	return &info, nil
}

// SetCPUBasicInfo sets the cpu basic info at the node-level annotations.
// It returns true if the annotations changes.
func SetCPUBasicInfo(annotations map[string]string, info *CPUBasicInfo) bool {
	b, _ := json.Marshal(info)
	s := string(b)

	if old := annotations[AnnotationCPUBasicInfo]; s == old {
		return false
	}

	annotations[AnnotationCPUBasicInfo] = s
	return true
}
