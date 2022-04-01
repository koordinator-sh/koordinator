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

package util

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func NewZeroResourceList() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewQuantity(0, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(0, resource.BinarySI),
	}
}

// MultiplyMilliQuant scales quantity by factor
func MultiplyMilliQuant(quant resource.Quantity, factor float64) resource.Quantity {
	milliValue := quant.MilliValue()
	newMilliValue := int64(float64(milliValue) * factor)
	newQuant := resource.NewMilliQuantity(newMilliValue, quant.Format)
	return *newQuant
}

// MultiplyQuant scales quantity by factor
func MultiplyQuant(quant resource.Quantity, factor float64) resource.Quantity {
	value := quant.Value()
	newValue := int64(float64(value) * factor)
	newQuant := resource.NewQuantity(newValue, quant.Format)
	return *newQuant
}

// GetMilliQuant returns the milli-valued quantity
func GetMilliQuant(quant resource.Quantity) resource.Quantity {
	milliValue := quant.MilliValue()
	newQuant := resource.NewQuantity(milliValue, quant.Format)
	return *newQuant
}

// IsResourceDiff returns whether the new resource has big enough difference with the old one or not
func IsResourceDiff(oldResourceList, newResourceList corev1.ResourceList, resourceName string, diffThreshold float64) bool {
	// consider the quantity of the non-exist resource as zero
	newQuant := 0.0
	newResource, newExist := newResourceList[corev1.ResourceName(resourceName)]
	if newExist {
		newQuant = float64(newResource.MilliValue())
	}

	oldQuant := 0.0
	oldResource, oldExist := oldResourceList[corev1.ResourceName(resourceName)]
	if oldExist {
		oldQuant = float64(oldResource.MilliValue())
	}

	if newExist != oldExist {
		return true
	}

	return newQuant >= oldQuant*(1+diffThreshold) || newQuant <= oldQuant*(1-diffThreshold)
}
