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

import corev1 "k8s.io/api/core/v1"

// NOTE: functions in this file can be overwritten for extension

var DefaultPriorityClass = PriorityNone

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
