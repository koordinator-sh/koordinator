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
	"math"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
)

func ValidateScaleDownBinPackArgs(path *field.Path, args *deschedulerconfig.ScaleDownBinPackArgs) error {
	var allErrs field.ErrorList

	if args.Strategy != deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly && args.Strategy != deschedulerconfig.ScaleDownBinPackStrategyEvictDirectly {
		allErrs = append(allErrs, field.Invalid(path.Child("strategy"), args.Strategy, "strategy must be CalculateOnly or EvictDirectly"))
	}

	if args.Strategy == deschedulerconfig.ScaleDownBinPackStrategyEvictDirectly {
		if args.MaxPodsToEvict == nil || *args.MaxPodsToEvict <= 0 {
			allErrs = append(allErrs, field.Invalid(path.Child("maxPodsToEvict"), args.MaxPodsToEvict, "maxPodsToEvict must be positive when strategy is EvictDirectly"))
		}
	}

	if args.NodeSelector != nil {
		if _, err := metav1.LabelSelectorAsSelector(args.NodeSelector); err != nil {
			allErrs = append(allErrs, field.Invalid(path.Child("nodeSelector"), args.NodeSelector, err.Error()))
		}
	}

	for i, v := range args.PodSelectors {
		if v.Selector != nil {
			if _, err := metav1.LabelSelectorAsSelector(v.Selector); err != nil {
				allErrs = append(allErrs, field.Invalid(path.Child("podSelectors").Index(i), v, err.Error()))
			}
		}
	}

	if args.EvictableNamespaces != nil && len(args.EvictableNamespaces.Include) > 0 && len(args.EvictableNamespaces.Exclude) > 0 {
		allErrs = append(allErrs, field.Invalid(path.Child("evictableNamespaces"), args.EvictableNamespaces, "only one of Include/Exclude namespaces can be set"))
	}

	for res, weight := range args.ResourceWeights {
		if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			allErrs = append(allErrs, field.Invalid(path.Child("resourceWeights").Key(string(res)), weight, "weights must be positive finite floats"))
		}
	}

	if len(allErrs) == 0 {
		return nil
	}
	return allErrs.ToAggregate()
}
