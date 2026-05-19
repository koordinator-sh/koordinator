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

package frameworkext

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func strPtr(s string) *string { return &s }

func TestBuildSandboxSchedulingHint(t *testing.T) {
	tests := []struct {
		name         string
		pod          *corev1.Pod
		diagnosis    *Diagnosis
		wantNil      bool
		wantReason   string
		wantNextStep string
		wantRuntime  extension.SandboxRuntimeClass
	}{
		{
			name: "gVisor pod with warm pool exhausted",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelSandboxRuntimeClass: "gvisor",
					},
				},
			},
			diagnosis: &Diagnosis{
				ScheduleDiagnosis: &ScheduleDiagnosis{
					NodeFailedDetails: v1alpha1.NodeFailedDetails{
						&v1alpha1.NodeFailedDetail{
							NodeFailedStatus: v1alpha1.NodeFailedStatus{
								Reason: "warm pool exhausted: no pre-warmed gVisor sandbox available",
							},
						},
					},
				},
			},
			wantNil:      false,
			wantReason:   "warmPoolExhausted",
			wantNextStep: "scale-out-warm-pool",
			wantRuntime:  extension.SandboxRuntimeGVisor,
		},
		{
			name: "Kata pod with GPU memory insufficient",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					RuntimeClassName: strPtr("kata-containers"),
				},
			},
			diagnosis: &Diagnosis{
				ScheduleDiagnosis: &ScheduleDiagnosis{
					NodeFailedDetails: v1alpha1.NodeFailedDetails{
						&v1alpha1.NodeFailedDetail{
							NodeFailedStatus: v1alpha1.NodeFailedStatus{
								Reason: "gpu memory insufficient: requested 16Gi, available 8Gi",
							},
						},
					},
				},
			},
			wantNil:      false,
			wantReason:   "gpuMemoryInsufficient",
			wantNextStep: "reduce-parallelism",
			wantRuntime:  extension.SandboxRuntimeKata,
		},
		{
			name: "Wasm pod with general capacity failure",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelSandboxRuntimeClass: "wasm",
					},
				},
			},
			diagnosis: &Diagnosis{
				ScheduleDiagnosis: &ScheduleDiagnosis{
					NodeFailedDetails: v1alpha1.NodeFailedDetails{
						&v1alpha1.NodeFailedDetail{
							NodeFailedStatus: v1alpha1.NodeFailedStatus{
								Reason: "insufficient cpu",
							},
						},
					},
				},
			},
			wantNil:      false,
			wantReason:   "nodeCapacityExceeded",
			wantNextStep: "wait-for-capacity",
			wantRuntime:  extension.SandboxRuntimeWasm,
		},
		{
			name: "non-sandbox pod produces no hint",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					RuntimeClassName: strPtr("runc"),
				},
			},
			diagnosis: &Diagnosis{
				ScheduleDiagnosis: &ScheduleDiagnosis{
					NodeFailedDetails: v1alpha1.NodeFailedDetails{
						&v1alpha1.NodeFailedDetail{
							NodeFailedStatus: v1alpha1.NodeFailedStatus{
								Reason: "insufficient cpu",
							},
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "sandbox pod with empty failure details produces no hint",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelSandboxRuntimeClass: "gvisor",
					},
				},
			},
			diagnosis: &Diagnosis{
				ScheduleDiagnosis: &ScheduleDiagnosis{
					NodeFailedDetails: v1alpha1.NodeFailedDetails{},
				},
			},
			wantNil: true,
		},
		{
			name: "nil TargetPod produces no hint",
			pod:  nil,
			diagnosis: &Diagnosis{
				ScheduleDiagnosis: &ScheduleDiagnosis{},
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.pod != nil {
				tt.diagnosis.TargetPod = tt.pod
			}
			got := BuildSandboxSchedulingHint(tt.diagnosis)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantReason, got.Reason)
			assert.Equal(t, tt.wantNextStep, got.NextStep)
			assert.Equal(t, tt.wantRuntime, got.Runtime)
		})
	}
}

func TestSetGetSandboxSchedulingHint_RoundTrip(t *testing.T) {
	pod := &corev1.Pod{}
	hint := extension.SandboxSchedulingHint{
		Reason:            "warmPoolExhausted",
		NextStep:          "scale-out-warm-pool",
		Runtime:           extension.SandboxRuntimeGVisor,
		SuggestedQoSClass: extension.QoSLS,
	}
	require.NoError(t, SetSandboxSchedulingHint(pod, hint))
	assert.NotEmpty(t, pod.Annotations[extension.AnnotationSandboxSchedulingHint])

	got, err := GetSandboxSchedulingHint(pod)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, hint.Reason, got.Reason)
	assert.Equal(t, hint.NextStep, got.NextStep)
	assert.Equal(t, hint.Runtime, got.Runtime)
	assert.Equal(t, hint.SuggestedQoSClass, got.SuggestedQoSClass)
}

func TestGetSandboxSchedulingHint_Absent(t *testing.T) {
	pod := &corev1.Pod{}
	got, err := GetSandboxSchedulingHint(pod)
	assert.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetSandboxSchedulingHint_Malformed(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				extension.AnnotationSandboxSchedulingHint: "not-valid-json{{",
			},
		},
	}
	got, err := GetSandboxSchedulingHint(pod)
	assert.Error(t, err)
	assert.Nil(t, got)
}

func TestSetSandboxSchedulingHint_NilAnnotations(t *testing.T) {
	pod := &corev1.Pod{}
	assert.Nil(t, pod.Annotations)
	hint := extension.SandboxSchedulingHint{Reason: "warmPoolExhausted", NextStep: "scale-out-warm-pool"}
	require.NoError(t, SetSandboxSchedulingHint(pod, hint))
	assert.NotNil(t, pod.Annotations)
	assert.NotEmpty(t, pod.Annotations[extension.AnnotationSandboxSchedulingHint])
}
