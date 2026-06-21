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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
)

func ValidateFragmentationAwareArgs(path *field.Path, args *deschedulerconfig.FragmentationAwareArgs) error {
	var allErrs field.ErrorList

	if args == nil {
		allErrs = append(allErrs, field.Required(path, "FragmentationAwareArgs must not be nil"))
		return allErrs.ToAggregate()
	}

	if len(args.Resources) == 0 {
		allErrs = append(allErrs, field.Required(path.Child("resources"), "resources must not be empty"))
	}

	resourceSet := sets.NewString()
	for i, res := range args.Resources {
		if resourceSet.Has(string(res)) {
			allErrs = append(allErrs, field.Duplicate(path.Child("resources").Index(i), res))
		}
		resourceSet.Insert(string(res))
	}

	if args.ImbalanceThreshold < 0 {
		allErrs = append(allErrs, field.Invalid(path.Child("imbalanceThreshold"), args.ImbalanceThreshold, "must be greater than or equal to 0"))
	}

	if args.MinImprovementThreshold < 0 {
		allErrs = append(allErrs, field.Invalid(path.Child("minImprovementThreshold"), args.MinImprovementThreshold, "must be greater than or equal to 0"))
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

	if len(allErrs) == 0 {
		return nil
	}
	return allErrs.ToAggregate()
}
