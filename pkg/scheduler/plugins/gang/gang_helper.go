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

package gang

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	v1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func getNamespaceSplicingName(namespace, name string) string {
	return namespace + "/" + name
}

func parsePgTimeoutSeconds(timeoutSeconds int32) (time.Duration, error) {
	if timeoutSeconds <= 0 {
		return 0, fmt.Errorf("podGroup timeout value is illegal,timeout Value:%v", timeoutSeconds)
	}
	return time.Duration(timeoutSeconds) * time.Second, nil
}

// GetPodSubPriority
// Get pod's sub-priority in Koordinator from label
func getPodSubPriority(pod *v1.Pod) (int32, error) {
	if pod.Labels[extension.LabelPodPriority] != "" {
		priority, err := strconv.ParseInt(pod.Labels[extension.LabelPodPriority], 0, 32)
		if err != nil {
			return 0, err
		}
		return int32(priority), nil
	}
	// When pod isn't set with the KoordinatorPriority label,
	// We assume that the sub-priority of the pod is default 0
	return 0, nil
}

func getGangNameByPod(pod *v1.Pod) string {
	if pod == nil {
		return ""
	}
	var gangName string
	gangName = pod.Labels[v1alpha1.PodGroupLabel]
	if gangName == "" {
		gangName = pod.Annotations[extension.AnnotationGangName]
	}
	return gangName
}

func generatePodPatch(oldPod, newPod *v1.Pod) ([]byte, error) {
	oldData, err := json.Marshal(oldPod)
	if err != nil {
		return nil, err
	}

	newData, err := json.Marshal(newPod)
	if err != nil {
		return nil, err
	}
	return strategicpatch.CreateTwoWayMergePatch(oldData, newData, &v1.Pod{})
}

// StringToGangGroupSlice
// Parse gang group's annotation like :"["nsA/gangA","nsB/gangB"]"  => goLang slice : []string{"nsA/gangA"."nsB/gangB"}
func stringToGangGroupSlice(s string) ([]string, error) {
	gangGroup := make([]string, 0)
	err := json.Unmarshal([]byte(s), &gangGroup)
	if err != nil {

		return gangGroup, err
	}
	return gangGroup, nil
}

func makePG(name, namespace string, min int32, creationTime *time.Time, minResource *v1.ResourceList) *v1alpha1.PodGroup {
	var ti int32 = 10
	pg := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       v1alpha1.PodGroupSpec{MinMember: min, ScheduleTimeoutSeconds: &ti},
	}
	if creationTime != nil {
		pg.CreationTimestamp = metav1.Time{Time: *creationTime}
	}
	if minResource != nil {
		pg.Spec.MinResources = minResource
	}
	return pg
}
