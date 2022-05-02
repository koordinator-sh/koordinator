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

package metriccache

import (
	"time"
)

const (
	NodeCPUInfoRecordType = "NodeCPUInfo"
)

type nodeResourceMetric struct {
	ID              uint64 `gorm:"primarykey"`
	CPUUsedCores    float64
	MemoryUsedBytes float64
	Timestamp       time.Time
}

type podResourceMetric struct {
	ID              uint64 `gorm:"primarykey"`
	PodUID          string `gorm:"index:idx_pod_res_uid"`
	CPUUsedCores    float64
	MemoryUsedBytes float64
	Timestamp       time.Time
}

type containerResourceMetric struct {
	ID              uint64 `gorm:"primarykey"`
	ContainerID     string `gorm:"index:idx_container_res_uid"`
	CPUUsedCores    float64
	MemoryUsedBytes float64
	Timestamp       time.Time
}

type podThrottledMetric struct {
	ID                uint64 `gorm:"primarykey"`
	PodUID            string `gorm:"index:idx_pod_throttled_uid"`
	CPUThrottledRatio float64
	Timestamp         time.Time
}

type containerThrottledMetric struct {
	ID                uint64 `gorm:"primarykey"`
	ContainerID       string `gorm:"index:idx_container_throttled_uid"`
	CPUThrottledRatio float64
	Timestamp         time.Time
}

type beCPUResourceMetric struct {
	ID              uint64 `gorm:"primarykey"`
	CPUUsedCores    float64
	CPULimitCores   float64
	CPURequestCores float64
	Timestamp       time.Time
}

type rawRecord struct {
	RecordType string `gorm:"primarykey"`
	RecordStr  string
}
