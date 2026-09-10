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
	tests := []struct {
		name           string
		labels         map[string]string
		specName       string
		expectedResult string
	}{
		{
			name: "scheduler name label takes precedence",
			labels: map[string]string{
				LabelSchedulerName: "custom-scheduler",
			},
			specName:       "default-scheduler",
			expectedResult: "custom-scheduler",
		},
		{
			name:           "sandbox scheduler name is used directly",
			specName:       SandboxSchedulerName,
			expectedResult: SandboxSchedulerName,
		},
		{
			name: "non-sandbox pod keeps spec scheduler",
			labels: map[string]string{
				"other-label": "value",
			},
			specName:       "default-scheduler",
			expectedResult: "default-scheduler",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: tt.labels},
				Spec:       corev1.PodSpec{SchedulerName: tt.specName},
			}
			assert.Equal(t, tt.expectedResult, GetSchedulerName(pod))
		})
	}
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

	// Test case 5: Pod with deprecated annotation only
	podWithDeprecatedAnnotation := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				DeprecatedAnnotationSchedulingHint: string(hintBytes),
			},
		},
	}

	result, err = GetSchedulingHint(podWithDeprecatedAnnotation)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, []string{"test-node"}, result.NodeNames)
	assert.Equal(t, schedulingHint.Extensions, result.Extensions)

	// Test case 6: Pod with both new and deprecated annotations, new annotation takes precedence
	differentHint := &SchedulingHint{
		NodeNames: []string{"new-node"},
		Extensions: map[string]interface{}{
			"newKey": "newValue",
		},
	}
	newHintBytes, err := json.Marshal(differentHint)
	assert.NoError(t, err)

	podWithBothAnnotations := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationSchedulingHint:           string(newHintBytes),
				DeprecatedAnnotationSchedulingHint: string(hintBytes),
			},
		},
	}

	result, err = GetSchedulingHint(podWithBothAnnotations)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, []string{"new-node"}, result.NodeNames, "New annotation should take precedence")
	assert.Equal(t, differentHint.Extensions, result.Extensions)

	// Test case 7: Pod with invalid deprecated annotation
	podWithInvalidDeprecated := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				DeprecatedAnnotationSchedulingHint: "invalid-json",
			},
		},
	}

	result, err = GetSchedulingHint(podWithInvalidDeprecated)
	assert.Error(t, err)
	assert.Nil(t, result, "Should return error for invalid deprecated JSON")

	// Test case 8: Pod without AnnotationSchedulingHint annotation
	podWithEmptyAnnotation := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationSchedulingHint: "",
			},
		},
	}

	result, err = GetSchedulingHint(podWithEmptyAnnotation)
	assert.NoError(t, err)
	assert.Nil(t, result, "Should return nil when annotation is empty")
}
