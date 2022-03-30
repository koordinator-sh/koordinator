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

type PriorityClass string

const (
	PriorityProd  PriorityClass = "koord-prod"
	PriorityMid   PriorityClass = "koord-mid"
	PriorityBatch PriorityClass = "koord-batch"
	PriorityFree  PriorityClass = "koord-free"
	PriorityNone  PriorityClass = ""

	PriorityProdValueMax int32 = 9999
	PriorityProdValueMin int32 = 9000

	PriorityMidValueMax int32 = 7099
	PriorityMidValueMin int32 = 7000

	PriorityBatchValueMax int32 = 6999
	PriorityBatchValueMin int32 = 6000

	PriorityFreeValueMax int32 = 3999
	PriorityFreeValueMin int32 = 3000
)

func GetPriorityClass(pod *corev1.Pod) PriorityClass {
	if pod == nil || pod.Spec.Priority == nil {
		return PriorityNone
	}
	return getPriorityClassByPriority(pod.Spec.Priority)
}

func getPriorityClassByPriority(priority *int32) PriorityClass {
	if priority == nil {
		return PriorityNone
	}

	p := *priority
	if p >= PriorityProdValueMin && p <= PriorityProdValueMax {
		return PriorityProd
	} else if p >= PriorityMidValueMin && p <= PriorityMidValueMax {
		return PriorityMid
	} else if p >= PriorityBatchValueMin && p <= PriorityBatchValueMax {
		return PriorityBatch
	} else if p >= PriorityFreeValueMin && p <= PriorityFreeValueMax {
		return PriorityFree
	}

	return PriorityNone
}
