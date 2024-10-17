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
	"encoding/json"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

// DefaultWaitTime is 60s if ScheduleTimeoutSeconds is not specified.
const DefaultWaitTime = 60 * time.Second

// CreateMergePatch return patch generated from original and new interfaces
func CreateMergePatch(original, new interface{}) ([]byte, error) {
	pvByte, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}
	cloneByte, err := json.Marshal(new)
	if err != nil {
		return nil, err
	}
	patch, err := strategicpatch.CreateTwoWayMergePatch(pvByte, cloneByte, original)
	if err != nil {
		return nil, err
	}
	return patch, nil
}

// GetPodGroupLabel get pod group from pod annotations
func GetPodGroupLabel(pod *v1.Pod) string {
	return pod.Labels[v1alpha1.PodGroupLabel]
}

// GetPodGroupFullName get namespaced group name from pod annotations
func GetPodGroupFullName(pod *v1.Pod) string {
	pgName := GetPodGroupLabel(pod)
	if len(pgName) == 0 {
		return ""
	}
	return fmt.Sprintf("%v/%v", pod.Namespace, pgName)
}

// GetWaitTimeDuration returns a wait timeout based on the following precedences:
// 1. spec.scheduleTimeoutSeconds of the given pg, if specified
// 2. given scheduleTimeout, if not nil
// 3. fall back to DefaultWaitTime
func GetWaitTimeDuration(pg *v1alpha1.PodGroup, scheduleTimeout *time.Duration) time.Duration {
	if pg != nil && pg.Spec.ScheduleTimeoutSeconds != nil {
		return time.Duration(*pg.Spec.ScheduleTimeoutSeconds) * time.Second
	}
	if scheduleTimeout != nil && *scheduleTimeout != 0 {
		return *scheduleTimeout
	}
	return DefaultWaitTime
}
