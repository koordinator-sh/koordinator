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

	"k8s.io/apimachinery/pkg/util/validation"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
)

// ValidateCustomPriorityArgs validates the CustomPriorityArgs
func ValidateCustomPriorityArgs(args *deschedulerconfig.CustomPriorityArgs) error {
	if args == nil {
		return fmt.Errorf("CustomPriorityArgs cannot be nil")
	}

	if len(args.EvictionOrder) < 2 {
		return fmt.Errorf("CustomPriority requires at least 2 resource priority levels")
	}

	// Validate each priority level
	priorityNames := make(map[string]bool)
	for i, priority := range args.EvictionOrder {
		if err := validateResourcePriority(priority, i); err != nil {
			return err
		}

		// Check for duplicate priority names
		if priorityNames[priority.Name] {
			return fmt.Errorf("duplicate priority name: %s", priority.Name)
		}
		priorityNames[priority.Name] = true

		if priority.NodeSelector == nil {
			return fmt.Errorf("priority %s must have a nodeSelector", priority.Name)
		}
	}

	// Validate pod selectors
	for _, selector := range args.PodSelectors {
		if err := validatePodSelector(selector); err != nil {
			return err
		}
	}

	// Validate mode
	switch args.Mode {
	case "", "BestEffort", "DrainNode":
		// empty handled by defaulting to BestEffort
	default:
		return fmt.Errorf("invalid mode: %s, supported: BestEffort, DrainNode", args.Mode)
	}

	return nil
}

// validateResourcePriority validates a single resource priority
func validateResourcePriority(priority deschedulerconfig.ResourcePriority, index int) error {
	if priority.Name == "" {
		return fmt.Errorf("priority[%d].name cannot be empty", index)
	}

	if errs := validation.IsDNS1123Subdomain(priority.Name); len(errs) > 0 {
		return fmt.Errorf("priority[%d].name %s is invalid: %v", index, priority.Name, errs)
	}

	if priority.NodeSelector == nil {
		return fmt.Errorf("priority[%d].nodeSelector cannot be nil", index)
	}

	// Validate thresholds if provided
	if priority.KeepThreshold != nil {
		if err := validateResourceThresholds(*priority.KeepThreshold); err != nil {
			return fmt.Errorf("priority[%d].keepThreshold is invalid: %v", index, err)
		}
	}

	if priority.RejectThreshold != nil {
		if err := validateResourceThresholds(*priority.RejectThreshold); err != nil {
			return fmt.Errorf("priority[%d].rejectThreshold is invalid: %v", index, err)
		}
	}

	return nil
}

// validatePodSelector validates a pod selector
func validatePodSelector(selector deschedulerconfig.CustomPriorityPodSelector) error {
	if selector.Name == "" {
		return fmt.Errorf("podSelector.name cannot be empty")
	}

	if errs := validation.IsDNS1123Subdomain(selector.Name); len(errs) > 0 {
		return fmt.Errorf("podSelector.name %s is invalid: %v", selector.Name, errs)
	}

	return nil
}

// validateResourceThresholds validates resource thresholds
func validateResourceThresholds(thresholds deschedulerconfig.ResourceThresholds) error {
	for resourceName, threshold := range thresholds {
		if threshold < 0 || threshold > 100 {
			return fmt.Errorf("resource threshold for %s must be between 0 and 100, got %f", resourceName, threshold)
		}
	}
	return nil
}
