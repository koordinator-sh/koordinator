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

package metricscollector

import (
	"k8s.io/apimachinery/pkg/types"
	"time"
)

type Sample interface {
	Value() (time.Time, float64)
}

type sample struct {
	timestamp time.Time
	value     float64
}

func (s *sample) Value() (time.Time, float64) {
	return s.timestamp, s.value
}

type MetricsServerRepository interface {
	Start() error
	GetAllContainerCPUUsage(containerName string, pods []types.NamespacedName) (map[types.NamespacedName]Sample, error)
	GetAllContainerMemoryUsage(containerName string, pods []types.NamespacedName) (map[types.NamespacedName]Sample, error)
}

type metricsServerRepoImpl struct {
}
