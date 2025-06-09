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
	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
)

const (
	AnnotationSkipUpdateResource = "config.koordinator.sh/skip-update-resources"

	// LabelControllerManaged indicates whether the colocation profile should be reconciled by the controller.
	// If not specified, the controller only reconciles the profile if ReconcileByDefault is set to true.
	LabelControllerManaged = "config.koordinator.sh/controller-managed"
)

func ShouldSkipUpdateResource(profile *configv1alpha1.ClusterColocationProfile) bool {
	if profile == nil || profile.Annotations == nil {
		return false
	}
	_, ok := profile.Annotations[AnnotationSkipUpdateResource]
	return ok
}

func ShouldReconcileProfile(profile *configv1alpha1.ClusterColocationProfile) bool {
	return profile != nil && profile.Labels != nil && profile.Labels[LabelControllerManaged] == "true"
}
