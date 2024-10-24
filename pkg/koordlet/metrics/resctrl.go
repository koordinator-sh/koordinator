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

package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	ResourceTypeLLC = "llc"
	ResourceTypeMB  = "mb"

	ResctrlResourceType = "resource_type"
	ResctrlCacheId      = "cache_id"
	ResctrlQos          = "qos"
	ResctrlMbType       = "mb_type"
)

var (
	QosResctrl = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "qos_resctrl",
		Help:      "qos resctrl collected by koordlet",
	}, []string{NodeKey, ResctrlResourceType, ResctrlCacheId, ResctrlQos, ResctrlMbType})

	ResctrlCollectors = []prometheus.Collector{
		QosResctrl,
	}
)

func ResetQosResctrl() {
	QosResctrl.Reset()
}

func RecordQosResctrl(resourceType string, cacheId int, qos, mbType string, value uint64) {
	labels := genNodeLabels()
	labels[ResctrlResourceType] = resourceType
	labels[ResctrlCacheId] = strconv.Itoa(cacheId)
	labels[ResctrlQos] = qos
	labels[ResctrlMbType] = mbType
	QosResctrl.With(labels).Set(float64(value))
}
