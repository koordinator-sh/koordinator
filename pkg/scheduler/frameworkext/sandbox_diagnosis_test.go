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
		{
			name: "gVisor pod with runtimeclass unavailable",
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
								Reason: "runtimeclass not found: gvisor",
							},
						},
					},
				},
			},
			wantNil:      false,
			wantReason:   "runtimeClassUnavailable",
			wantNextStep: "use-different-node-pool",
			wantRuntime:  extension.SandboxRuntimeGVisor,
		},
		{
			name: "nil ScheduleDiagnosis produces no hint",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelSandboxRuntimeClass: "gvisor",
					},
				},
			},
			diagnosis: &Diagnosis{
				ScheduleDiagnosis: nil,
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

func TestSandboxDiagnosisProcessor_NilPod(t *testing.T) {
	// Should not panic with nil TargetPod
	sandboxDiagnosisProcessor(&Diagnosis{TargetPod: nil})
}

func TestSandboxDiagnosisProcessor_NonSandboxPod(t *testing.T) {
	// Should return early without logging for non-sandbox pods
	sandboxDiagnosisProcessor(&Diagnosis{
		TargetPod: &corev1.Pod{
			Spec: corev1.PodSpec{RuntimeClassName: strPtr("runc")},
		},
		ScheduleDiagnosis: &ScheduleDiagnosis{
			NodeFailedDetails: v1alpha1.NodeFailedDetails{
				&v1alpha1.NodeFailedDetail{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{Reason: "insufficient cpu"},
				},
			},
		},
	})
}

// TestSandboxDiagnosisProcessor_ValidSandboxHint covers the json.Marshal and
// klog.V(4).Infof lines inside sandboxDiagnosisProcessor when BuildSandboxSchedulingHint
// returns a non-nil hint. These lines were previously uncovered.
func TestSandboxDiagnosisProcessor_ValidSandboxHint(t *testing.T) {
	sandboxDiagnosisProcessor(&Diagnosis{
		TargetPod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-sandbox",
				Namespace: "default",
				Labels: map[string]string{
					extension.LabelSandboxRuntimeClass: "gvisor",
				},
			},
		},
		ScheduleDiagnosis: &ScheduleDiagnosis{
			NodeFailedDetails: v1alpha1.NodeFailedDetails{
				&v1alpha1.NodeFailedDetail{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						Reason: "warm pool exhausted",
					},
				},
			},
		},
	})
	// No panic and no assertion needed: this test exists purely for
	// coverage of the json.Marshal + klog happy path.
}

// TestBuildSandboxSchedulingHint_NilDetailSkipped covers the
// `if detail == nil { continue }` guard inside sandboxFailureReason.
func TestBuildSandboxSchedulingHint_NilDetailSkipped(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				extension.LabelSandboxRuntimeClass: "gvisor",
			},
		},
	}
	diagnosis := &Diagnosis{
		TargetPod: pod,
		ScheduleDiagnosis: &ScheduleDiagnosis{
			NodeFailedDetails: v1alpha1.NodeFailedDetails{
				nil, // nil entry must be skipped without panic
				&v1alpha1.NodeFailedDetail{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						Reason: "insufficient memory",
					},
				},
			},
		},
	}
	hint := BuildSandboxSchedulingHint(diagnosis)
	require.NotNil(t, hint)
	assert.Equal(t, "nodeCapacityExceeded", hint.Reason)
	assert.Equal(t, "wait-for-capacity", hint.NextStep)
}

// TestBuildSandboxSchedulingHint_UnknownReasonDefaultsToCapacity covers the
// final default return at the bottom of sandboxFailureReason, reached when
// the failure message matches none of the known keyword patterns.
func TestBuildSandboxSchedulingHint_UnknownReasonDefaultsToCapacity(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				extension.LabelSandboxRuntimeClass: "kata-containers",
			},
		},
	}
	diagnosis := &Diagnosis{
		TargetPod: pod,
		ScheduleDiagnosis: &ScheduleDiagnosis{
			NodeFailedDetails: v1alpha1.NodeFailedDetails{
				&v1alpha1.NodeFailedDetail{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						Reason: "node affinity not satisfied",
					},
				},
			},
		},
	}
	hint := BuildSandboxSchedulingHint(diagnosis)
	require.NotNil(t, hint)
	assert.Equal(t, "nodeCapacityExceeded", hint.Reason)
	assert.Equal(t, "wait-for-capacity", hint.NextStep)
	assert.Equal(t, extension.SandboxRuntimeKata, hint.Runtime)
}

// TestBuildSandboxSchedulingHint_RuntimeClassWithSpace covers the right side
// of the OR in the runtimeclass detection branch:
// strings.Contains(msg, "runtimeclass") || strings.Contains(msg, "runtime class")
// The existing test only covered "runtimeclass" (no space). This covers "runtime class".
func TestBuildSandboxSchedulingHint_RuntimeClassWithSpace(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				extension.LabelSandboxRuntimeClass: "gvisor",
			},
		},
	}
	diagnosis := &Diagnosis{
		TargetPod: pod,
		ScheduleDiagnosis: &ScheduleDiagnosis{
			NodeFailedDetails: v1alpha1.NodeFailedDetails{
				&v1alpha1.NodeFailedDetail{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						Reason: "runtime class not installed on any node",
					},
				},
			},
		},
	}
	hint := BuildSandboxSchedulingHint(diagnosis)
	require.NotNil(t, hint)
	assert.Equal(t, "runtimeClassUnavailable", hint.Reason)
	assert.Equal(t, "use-different-node-pool", hint.NextStep)
}

// TestBuildSandboxSchedulingHint_PoolKeywordAlone covers the right side
// of the OR in the warmPool detection branch:
// strings.Contains(msg, "warm") || strings.Contains(msg, "pool")
// The existing test used "warm pool exhausted" which hit both sides.
// This test hits only the "pool" side.
func TestBuildSandboxSchedulingHint_PoolKeywordAlone(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				extension.LabelSandboxRuntimeClass: "wasm",
			},
		},
	}
	diagnosis := &Diagnosis{
		TargetPod: pod,
		ScheduleDiagnosis: &ScheduleDiagnosis{
			NodeFailedDetails: v1alpha1.NodeFailedDetails{
				&v1alpha1.NodeFailedDetail{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						Reason: "sandbox pool has no available slots",
					},
				},
			},
		},
	}
	hint := BuildSandboxSchedulingHint(diagnosis)
	require.NotNil(t, hint)
	assert.Equal(t, "warmPoolExhausted", hint.Reason)
	assert.Equal(t, "scale-out-warm-pool", hint.NextStep)
	assert.Equal(t, extension.SandboxRuntimeWasm, hint.Runtime)
}

// TestClassifyFailureMessage tests the classification function directly,
// independent of the Diagnosis struct. This verifies pattern precedence —
// in particular that GPU memory failures are not misclassified as generic
// capacity failures even when the message also contains "insufficient".
func TestClassifyFailureMessage(t *testing.T) {
	tests := []struct {
		name       string
		msg        string
		wantReason string
		wantNext   string
	}{
		{
			name:       "insufficient gpu memory hits gpuMemory not capacity",
			msg:        "insufficient gpu memory: 16Gi requested, 8Gi available",
			wantReason: "gpuMemoryInsufficient",
			wantNext:   "reduce-parallelism",
		},
		{
			name:       "warm pool exhausted",
			msg:        "warm pool has no available slots",
			wantReason: "warmPoolExhausted",
			wantNext:   "scale-out-warm-pool",
		},
		{
			name:       "pool keyword alone",
			msg:        "sandbox pool exhausted for runtime gvisor",
			wantReason: "warmPoolExhausted",
			wantNext:   "scale-out-warm-pool",
		},
		{
			name:       "runtimeclass not found",
			msg:        "runtimeclass gvisor not found on node",
			wantReason: "runtimeClassUnavailable",
			wantNext:   "use-different-node-pool",
		},
		{
			name:       "runtime class with space",
			msg:        "runtime class not installed on any node in pool",
			wantReason: "runtimeClassUnavailable",
			wantNext:   "use-different-node-pool",
		},
		{
			name:       "generic insufficient cpu",
			msg:        "insufficient cpu: 4 requested, 2 available",
			wantReason: "nodeCapacityExceeded",
			wantNext:   "wait-for-capacity",
		},
		{
			name:       "capacity exceeded",
			msg:        "node capacity exceeded for memory",
			wantReason: "nodeCapacityExceeded",
			wantNext:   "wait-for-capacity",
		},
		{
			name:       "unknown message returns empty",
			msg:        "node affinity not satisfied",
			wantReason: "",
			wantNext:   "",
		},
		{
			name:       "case insensitive matching",
			msg:        "GPU MEMORY INSUFFICIENT ON ALL NODES",
			wantReason: "gpuMemoryInsufficient",
			wantNext:   "reduce-parallelism",
		},
		{
			name:       "empty message returns empty",
			msg:        "",
			wantReason: "",
			wantNext:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReason, gotNext := classifyFailureMessage(tt.msg)
			if gotReason != tt.wantReason {
				t.Errorf("reason = %q, want %q", gotReason, tt.wantReason)
			}
			if gotNext != tt.wantNext {
				t.Errorf("nextStep = %q, want %q", gotNext, tt.wantNext)
			}
		})
	}
}
