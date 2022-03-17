package common

import corev1 "k8s.io/api/core/v1"

type PriorityClass string

const (
	PriorityProd  PriorityClass = "Prod"
	PriorityMid   PriorityClass = "Mid"
	PriorityBatch PriorityClass = "Batch"
	PriorityFree  PriorityClass = "Free"
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
