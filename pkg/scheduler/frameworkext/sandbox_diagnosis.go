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
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func init() {
	RegisterCustomDiagnosisProcessor("sandbox-scheduling-hint", sandboxDiagnosisProcessor)
}

// sandboxDiagnosisProcessor is registered via RegisterCustomDiagnosisProcessor.
// It derives a SandboxSchedulingHint from the Diagnosis and logs it at V(4).
// Annotation patching onto the live pod object is handled by PostFilter plugins
// that hold a Kubernetes client - not here.
func sandboxDiagnosisProcessor(diagnosis *Diagnosis) {
	if diagnosis.TargetPod == nil {
		return
	}
	hint := BuildSandboxSchedulingHint(diagnosis)
	if hint == nil {
		return
	}
	data, err := json.Marshal(hint)
	if err != nil {
		klog.V(4).Infof("sandbox_diagnosis: failed to marshal hint for pod %s/%s: %v",
			diagnosis.TargetPod.Namespace, diagnosis.TargetPod.Name, err)
		return
	}
	klog.V(4).Infof("sandbox_diagnosis: hint for pod %s/%s: %s",
		diagnosis.TargetPod.Namespace, diagnosis.TargetPod.Name, string(data))
}

// BuildSandboxSchedulingHint derives a SandboxSchedulingHint from a Diagnosis.
// Returns nil if the pod is not a known sandbox runtime or no hint can be derived.
func BuildSandboxSchedulingHint(diagnosis *Diagnosis) *extension.SandboxSchedulingHint {
	if diagnosis == nil || diagnosis.TargetPod == nil {
		return nil
	}
	rc := sandboxRuntimeClass(diagnosis.TargetPod)
	if !extension.KnownSandboxRuntimeClass(rc) {
		return nil
	}
	src := extension.SandboxRuntimeClass(rc)
	reason, nextStep := sandboxFailureReason(diagnosis)
	if reason == "" {
		return nil
	}
	return &extension.SandboxSchedulingHint{
		Reason:            reason,
		NextStep:          nextStep,
		Runtime:           src,
		SuggestedQoSClass: extension.DefaultQoSClassForSandboxRuntime(src),
	}
}

// sandboxRuntimeClass returns the sandbox runtime class name for a pod,
// checking the Koordinator sandbox label first, then spec.runtimeClassName.
func sandboxRuntimeClass(pod *corev1.Pod) string {
	if rc, ok := pod.Labels[extension.LabelSandboxRuntimeClass]; ok && rc != "" {
		return rc
	}
	if pod.Spec.RuntimeClassName != nil && *pod.Spec.RuntimeClassName != "" {
		return *pod.Spec.RuntimeClassName
	}
	return ""
}

// sandboxFailureReason maps NodeFailedDetails failure messages to
// machine-readable reason and nextStep strings for sandbox pods.
func sandboxFailureReason(diagnosis *Diagnosis) (reason, nextStep string) {
	if diagnosis.ScheduleDiagnosis == nil || len(diagnosis.ScheduleDiagnosis.NodeFailedDetails) == 0 {
		return "", ""
	}
	for _, detail := range diagnosis.ScheduleDiagnosis.NodeFailedDetails {
		if detail == nil {
			continue
		}
		msg := strings.ToLower(detail.Reason)
		switch {
		case strings.Contains(msg, "warm") || strings.Contains(msg, "pool"):
			return "warmPoolExhausted", "scale-out-warm-pool"
		case strings.Contains(msg, "gpu") && strings.Contains(msg, "memory"):
			return "gpuMemoryInsufficient", "reduce-parallelism"
		case strings.Contains(msg, "runtimeclass") || strings.Contains(msg, "runtime class"):
			return "runtimeClassUnavailable", "use-different-node-pool"
		case strings.Contains(msg, "insufficient") || strings.Contains(msg, "capacity"):
			return "nodeCapacityExceeded", "wait-for-capacity"
		}
	}
	// Known sandbox pod with unrecognized failure message - default to capacity.
	return "nodeCapacityExceeded", "wait-for-capacity"
}

// SetSandboxSchedulingHint JSON-encodes hint and writes it to the pod's
// AnnotationSandboxSchedulingHint annotation. Called by PostFilter plugins
// that hold a Kubernetes client for the actual patch.
func SetSandboxSchedulingHint(pod *corev1.Pod, hint extension.SandboxSchedulingHint) error {
	data, err := json.Marshal(hint)
	if err != nil {
		return fmt.Errorf("marshal SandboxSchedulingHint: %w", err)
	}
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[extension.AnnotationSandboxSchedulingHint] = string(data)
	return nil
}

// GetSandboxSchedulingHint reads and JSON-decodes the SandboxSchedulingHint
// from the pod annotation. Returns nil, nil if the annotation is absent.
func GetSandboxSchedulingHint(pod *corev1.Pod) (*extension.SandboxSchedulingHint, error) {
	if pod.Annotations == nil {
		return nil, nil
	}
	raw, ok := pod.Annotations[extension.AnnotationSandboxSchedulingHint]
	if !ok {
		return nil, nil
	}
	var hint extension.SandboxSchedulingHint
	if err := json.Unmarshal([]byte(raw), &hint); err != nil {
		return nil, fmt.Errorf("unmarshal SandboxSchedulingHint: %w", err)
	}
	return &hint, nil
}
