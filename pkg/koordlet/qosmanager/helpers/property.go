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

package helpers

import (
	corev1 "k8s.io/api/core/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func GetPodResourceQoSByQoSClass(pod *corev1.Pod, strategy *slov1alpha1.ResourceQOSStrategy) *slov1alpha1.ResourceQOS {
	if strategy == nil {
		return nil
	}
	var resourceQoS *slov1alpha1.ResourceQOS
	podQoS := apiext.GetPodQoSClassWithDefault(pod)
	switch podQoS {
	case apiext.QoSLSE:
		// currently LSE pods use the same strategy with LSR
		resourceQoS = strategy.LSRClass
	case apiext.QoSLSR:
		resourceQoS = strategy.LSRClass
	case apiext.QoSLS:
		resourceQoS = strategy.LSClass
	case apiext.QoSBE:
		resourceQoS = strategy.BEClass
	case apiext.QoSSystem:
		resourceQoS = strategy.SystemClass
	default:
		// should never reach here
	}
	return resourceQoS
}

// GetKubeQoSResourceQoSByQoSClass gets pod config by mapping kube qos into koordinator qos.
// https://koordinator.sh/docs/core-concepts/qos/#koordinator-qos-vs-kubernetes-qos
func GetKubeQoSResourceQoSByQoSClass(qosClass corev1.PodQOSClass, strategy *slov1alpha1.ResourceQOSStrategy) *slov1alpha1.ResourceQOS {
	// NOTE: only used for static qos resource calculation here, and it may be incorrect mapping for dynamic qos
	// resource, e.g. qos class of a LS pod can be corev1.PodQOSGuaranteed
	if strategy == nil {
		return nil
	}
	var resourceQoS *slov1alpha1.ResourceQOS
	switch qosClass {
	case corev1.PodQOSGuaranteed:
		resourceQoS = strategy.LSRClass
	case corev1.PodQOSBurstable:
		resourceQoS = strategy.LSClass
	case corev1.PodQOSBestEffort:
		resourceQoS = strategy.BEClass
	}
	return resourceQoS
}
