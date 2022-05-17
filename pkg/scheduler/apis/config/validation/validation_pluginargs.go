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

package validation

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

// ValidateLoadAwareSchedulingArgs validates that LoadAwareSchedulingArgs are correct.
func ValidateLoadAwareSchedulingArgs(args *config.LoadAwareSchedulingArgs) error {
	var allErrs field.ErrorList

	if args.NodeMetricUpdateMaxWindowSeconds <= 0 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("nodeMetricUpdateMaxWindowSeconds"), args.NodeMetricUpdateMaxWindowSeconds, "NodeMetricUpdateMaxWindowSeconds should be a positive value"))
	}

	if err := validateResourceWeights(args.ResourceWeights); err != nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("resourceWeights"), args.ResourceWeights, err.Error()))
	}
	if err := validateResourceThresholds(args.UsageThresholds); err != nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("usageThresholds"), args.UsageThresholds, err.Error()))
	}
	if err := validateResourceThresholds(args.EstimatedScalingFactors); err != nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("estimatedScalingFactors"), args.EstimatedScalingFactors, err.Error()))
	}
	if len(allErrs) == 0 {
		return nil
	}
	return allErrs.ToAggregate()
}

func validateResourceWeights(resources map[corev1.ResourceName]int64) error {
	for resourceName, weight := range resources {
		if weight <= 0 {
			return fmt.Errorf("resource Weight of %v should be a positive value, got %v", resourceName, weight)
		}
		if weight > 100 {
			return fmt.Errorf("resource Weight of %v should be less than 100, got %v", resourceName, weight)
		}
	}
	return nil
}

func validateResourceThresholds(thresholds map[corev1.ResourceName]int64) error {
	for resourceName, thresholdPercent := range thresholds {
		if thresholdPercent <= 0 {
			return fmt.Errorf("resource Threshold of %v should be a positive value, got %v", resourceName, thresholdPercent)
		}
		if thresholdPercent > 100 {
			return fmt.Errorf("resource Threshold of %v should be less than 100, got %v", resourceName, thresholdPercent)
		}
	}
	return nil
}
