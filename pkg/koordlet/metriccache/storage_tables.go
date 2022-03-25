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
