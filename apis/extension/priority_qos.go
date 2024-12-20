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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

type PriorityQoSClass string

const (
	PriorityQoSSeparator   = "/"
	PriorityQoSClassSystem = PriorityQoSClass(QoSSystem)
	PriorityQoSClassNone   = PriorityQoSClass("")
)

// Supported combinations:
// SYSTEM
// koord-prod: LS LSE LSR
// koord-mid: LS BE
// koord-batch: BE
// koord-free: BE
var (
	validCombinations = map[PriorityClass]map[QoSClass]bool{
		PriorityProd: {
			QoSLSE: true,
			QoSLSR: true,
			QoSLS:  true,
		},
		PriorityMid: {
			QoSLS: true,
			QoSBE: true,
		},
		PriorityBatch: {
			QoSBE: true,
		},
		PriorityFree: {
			QoSBE: true,
		},
	}
)

// GetPriorityQoSClass returns a valid combination of priorityClass and qosClass.
func GetPriorityQoSClass(priority PriorityClass, qos QoSClass) PriorityQoSClass {
	// When the pod's QoS is system, return PriorityQoSClassSystem
	if qos == QoSSystem {
		return PriorityQoSClassSystem
	}

	// The default qos is QoSLS, default priority is PriorityProd
	if qos == QoSNone {
		qos = QoSLS
	}
	if priority == PriorityNone {
		priority = PriorityProd
	}

	if !isValidCombination(priority, qos) {
		klog.Warningf("invalid combination of priorityClass:%s and qosClass:%s", string(priority), string(qos))
		return PriorityQoSClassNone
	}
	return PriorityQoSClass(string(priority) + PriorityQoSSeparator + string(qos))
}

func GetPriorityQosClassByName(priority, qos string) PriorityQoSClass {
	return GetPriorityQoSClass(PriorityClass(priority), QoSClass(qos))
}

func GetPodPriorityQoSClassRaw(pod *corev1.Pod) PriorityQoSClass {
	if pod == nil {
		return PriorityQoSClassNone
	}
	return GetPriorityQoSClass(GetPodPriorityClassRaw(pod), GetPodQoSClassRaw(pod))
}

func isValidCombination(priority PriorityClass, qos QoSClass) bool {
	return validCombinations[priority][qos]
}
