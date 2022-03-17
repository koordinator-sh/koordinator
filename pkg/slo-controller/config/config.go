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

package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

type ColocationCfg struct {
	Enable *bool `json:"enable,omitempty"`
	ColocationStrategy
	NodeConfigs []NodeColocationCfg `json:"nodeConfigs,omitempty"`
}

type ColocationStrategy struct {
	CPUReclaimThresholdPercent    *int64   `json:"cpuReclaimThresholdPercent,omitempty"`
	MemoryReclaimThresholdPercent *int64   `json:"memoryReclaimThresholdPercent,omitempty"`
	DegradeTimeMinutes            *int64   `json:"degradeTimeMinutes,omitempty"`
	UpdateTimeThresholdSeconds    *int64   `json:"updateTimeThresholdSeconds,omitempty"`
	ResourceDiffThreshold         *float64 `json:"resourceDiffThreshold,omitempty"`
}

type NodeColocationCfg struct {
	NodeSelector *metav1.LabelSelector
	ColocationCfg
}

func NewDefaultColocationCfg() *ColocationCfg {
	defaultCfg := DefaultColocationCfg()
	return &defaultCfg
}

func DefaultColocationCfg() ColocationCfg {
	return ColocationCfg{
		Enable:             pointer.BoolPtr(false),
		ColocationStrategy: DefaultColocationStrategy(),
	}
}

func DefaultColocationStrategy() ColocationStrategy {
	return ColocationStrategy{
		CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
		MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
		DegradeTimeMinutes:            pointer.Int64Ptr(15),
		UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
		ResourceDiffThreshold:         pointer.Float64(0.1),
	}
}
