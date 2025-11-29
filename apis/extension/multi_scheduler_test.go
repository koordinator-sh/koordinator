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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetSchedulerName(t *testing.T) {
	// Test case 1: Pod with LabelSchedulerName label
	podWithLabel := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				LabelSchedulerName: "custom-scheduler",
			},
		},
		Spec: corev1.PodSpec{
			SchedulerName: "default-scheduler",
		},
	}

	result := GetSchedulerName(podWithLabel)
	assert.Equal(t, "custom-scheduler", result, "Should return scheduler name from label when present")

	// Test case 2: Pod without LabelSchedulerName label
	podWithoutLabel := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"other-label": "value",
			},
		},
		Spec: corev1.PodSpec{
			SchedulerName: "default-scheduler",
		},
	}

	result = GetSchedulerName(podWithoutLabel)
	assert.Equal(t, "default-scheduler", result, "Should return spec.SchedulerName when label is not present")
}

func TestGetSchedulingHint(t *testing.T) {
	// Test case 1: Nil pod
	result, err := GetSchedulingHint(nil)
	assert.NoError(t, err)
	assert.Nil(t, result, "Should return nil for nil pod")

	// Test case 2: Pod without AnnotationSchedulingHint annotation
	podWithoutAnnotation := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"other-annotation": "value",
			},
		},
	}

	result, err = GetSchedulingHint(podWithoutAnnotation)
	assert.NoError(t, err)
	assert.Nil(t, result, "Should return nil when annotation is not present")

	// Test case 3: Pod with valid scheduling hint
	schedulingHint := &SchedulingHint{
		NodeNames: []string{"test-node"},
		Extensions: map[string]interface{}{
			"key1": "value1",
			"key2": "123",
		},
	}

	hintBytes, err := json.Marshal(schedulingHint)
	assert.NoError(t, err)

	podWithValidHint := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationSchedulingHint: string(hintBytes),
			},
		},
	}

	result, err = GetSchedulingHint(podWithValidHint)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, []string{"test-node"}, result.NodeNames)
	assert.Equal(t, schedulingHint.Extensions, result.Extensions)

	// Test case 4: Pod with invalid scheduling hint JSON
	podWithInvalidHint := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationSchedulingHint: "invalid-json",
			},
		},
	}

	result, err = GetSchedulingHint(podWithInvalidHint)
	assert.Error(t, err)
	assert.Nil(t, result, "Should return error for invalid JSON")
}
