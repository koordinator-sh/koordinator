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

type NetQoSClass string

const (
	NETQoSHigh NetQoSClass = "high_class"
	NETQoSMid  NetQoSClass = "mid_class"
	NETQoSLow  NetQoSClass = "low_class"
	NETQoSNone NetQoSClass = ""
)

func GetPodNetQoSClassByName(qos string) NetQoSClass {
	q := NetQoSClass(qos)

	switch q {
	case NETQoSHigh, NETQoSMid, NETQoSLow, NETQoSNone:
		return q
	}

	return NETQoSNone
}

func GetPodNetQoSClass(pod *corev1.Pod) NetQoSClass {
	if pod == nil || pod.Labels == nil {
		return NETQoSNone
	}
	return GetNetQoSClassByAttrs(pod.Labels, pod.Annotations)
}

func GetNetQoSClassByAttrs(labels, annotations map[string]string) NetQoSClass {
	// annotations are for old format adaption reason
	if q, exist := labels[LabelPodNetQoS]; exist {
		return GetPodNetQoSClassByName(q)
	}
	return NETQoSNone
}
