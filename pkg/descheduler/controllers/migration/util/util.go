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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sev1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/reservation"
)

func GetCondition(status *sev1alpha1.PodMigrationJobStatus, conditionType sev1alpha1.PodMigrationJobConditionType) (int, *sev1alpha1.PodMigrationJobCondition) {
	if len(status.Conditions) == 0 {
		return -1, nil
	}
	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return i, &status.Conditions[i]
		}
	}
	return -1, nil
}

func UpdateCondition(status *sev1alpha1.PodMigrationJobStatus, condition *sev1alpha1.PodMigrationJobCondition) bool {
	condition.LastTransitionTime = metav1.Now()
	// Try to find this PodMigrationJob condition.
	conditionIndex, oldCondition := GetCondition(status, condition.Type)

	if oldCondition == nil {
		// We are adding new PodMigrationJob condition.
		status.Conditions = append(status.Conditions, *condition)
		return true
	}
	// We are updating an existing condition, so we need to check if it has changed.
	if condition.Status == oldCondition.Status {
		condition.LastTransitionTime = oldCondition.LastTransitionTime
	}

	isEqual := condition.Status == oldCondition.Status &&
		condition.Reason == oldCondition.Reason &&
		condition.Message == oldCondition.Message &&
		condition.LastProbeTime.Equal(&oldCondition.LastProbeTime) &&
		condition.LastTransitionTime.Equal(&oldCondition.LastTransitionTime)

	status.Conditions[conditionIndex] = *condition
	// Return true if one of the fields have changed.
	return !isEqual
}

func IsMigratePendingPod(reservationObj reservation.Object) bool {
	pending := false
	for _, v := range reservationObj.GetReservationOwners() {
		if v.Object != nil && v.Controller == nil && v.LabelSelector == nil {
			pending = true
			break
		}
	}
	return pending
}
