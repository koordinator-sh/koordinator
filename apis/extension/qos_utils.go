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
	v1qos "k8s.io/kubernetes/pkg/apis/core/v1/helper/qos"
)

// NOTE: functions in this file can be overwritten for extension

// QoSClassForGuaranteed indicates the QoSClass which a Guaranteed Pod without a koordinator QoSClass specified should
// be regarded by default.
// TODO: add component options to customize it.
var QoSClassForGuaranteed = QoSLSR

// GetPodQoSClassWithDefault gets the pod's QoSClass with the default config.
func GetPodQoSClassWithDefault(pod *corev1.Pod) QoSClass {
	qosClass := GetPodQoSClassRaw(pod)
	if qosClass != QoSNone {
		return qosClass
	}

	return GetPodQoSClassWithKubeQoS(GetKubeQosClass(pod))
}

// GetPodQoSClassWithKubeQoS returns the default QoSClass according to its kubernetes QoSClass when the pod does not
// specify a koordinator QoSClass explicitly.
// https://koordinator.sh/docs/architecture/qos#koordinator-qos-vs-kubernetes-qos
func GetPodQoSClassWithKubeQoS(kubeQOS corev1.PodQOSClass) QoSClass {
	switch kubeQOS {
	case corev1.PodQOSGuaranteed:
		return QoSClassForGuaranteed
	case corev1.PodQOSBurstable:
		return QoSLS
	case corev1.PodQOSBestEffort:
		return QoSBE
	}
	// should never reach here
	return QoSNone
}

func GetPodQoSClassRaw(pod *corev1.Pod) QoSClass {
	if pod == nil || pod.Labels == nil {
		return QoSNone
	}
	return GetQoSClassByAttrs(pod.Labels, pod.Annotations)
}

func GetQoSClassByAttrs(labels, annotations map[string]string) QoSClass {
	// annotations are for old format adaption reason
	if q, exist := labels[LabelPodQoS]; exist {
		return GetPodQoSClassByName(q)
	}
	return QoSNone
}

func GetKubeQosClass(pod *corev1.Pod) corev1.PodQOSClass {
	qosClass := pod.Status.QOSClass
	if len(qosClass) > 0 {
		return qosClass
	}
	return v1qos.GetPodQOS(pod)
}
