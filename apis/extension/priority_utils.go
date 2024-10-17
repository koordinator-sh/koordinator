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
	"k8s.io/utils/pointer"
)

// NOTE: functions in this file can be overwritten for extension

var (
	DefaultPriorityClass = PriorityNone

	PriorityProdValueDefault  int32 = 9500
	PriorityMidValueDefault   int32 = 7500
	PriorityBatchValueDefault int32 = 5500
	PriorityFreeValueDefault  int32 = 3500
	PriorityNoneValueDefault  int32 = 0
)

// GetPodPriorityClassWithDefault gets the pod's PriorityClass with the default config.
func GetPodPriorityClassWithDefault(pod *corev1.Pod) PriorityClass {
	priorityClass := GetPodPriorityClassRaw(pod)
	if priorityClass != PriorityNone {
		return priorityClass
	}

	return GetPodPriorityClassWithQoS(GetPodQoSClassWithDefault(pod))
}

// GetPodPriorityClassWithQoS returns the default PriorityClass according to its QoSClass when the pod does not specify
// a PriorityClass explicitly.
// Note that this is only a derivation of the default value, and the reverse is not true. For example, PriorityMid
// can also be combined with QoSLS.
func GetPodPriorityClassWithQoS(qos QoSClass) PriorityClass {
	switch qos {
	case QoSSystem, QoSLSE, QoSLSR, QoSLS:
		return PriorityProd
	case QoSBE:
		return PriorityBatch
	}
	return DefaultPriorityClass
}

// GetPodPriorityValueWithDefault returns a priority value for the pod according to its koordinator priority classes.
// If the pod has a non-zero priority value, it directly returns. If the pod has a koordinator priority class but
// priority value not set, it returns the default value of the class. If the pod neither sets a non-zero priority
// value nor has a valid koordinator priority class, it uses the default value of the DefaultPriorityClass.
func GetPodPriorityValueWithDefault(pod *corev1.Pod) *int32 {
	if pod == nil {
		return pointer.Int32(PriorityNoneValueDefault)
	}

	// if there is a non-default priority value, use it
	if p := pod.Spec.Priority; p != nil && *p != PriorityNoneValueDefault {
		return p
	}

	priorityClass := GetPodPriorityClassWithDefault(pod)
	return pointer.Int32(GetDefaultPriorityByPriorityClass(priorityClass))
}

func GetDefaultPriorityByPriorityClass(priorityClass PriorityClass) int32 {
	switch priorityClass {
	case PriorityProd:
		return PriorityProdValueDefault
	case PriorityMid:
		return PriorityMidValueDefault
	case PriorityBatch:
		return PriorityBatchValueDefault
	case PriorityFree:
		return PriorityFreeValueDefault
	default:
		return PriorityNoneValueDefault
	}
}
