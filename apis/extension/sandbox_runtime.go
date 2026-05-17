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

const (
	// LabelSandboxRuntimeClass marks a pod as running in an AI agent sandbox.
	// The value is the RuntimeClass name (e.g. "gvisor", "kata-containers", "wasm").
	LabelSandboxRuntimeClass = "koordinator.sh/sandbox-runtime-class"

	// AnnotationSandboxPipelineName links a pod to a SandboxPipeline custom resource,
	// enabling pre-warming and capacity reservation via the Sandbox Pipeline mechanism.
	AnnotationSandboxPipelineName = "scheduling.koordinator.sh/sandbox-pipeline"

	// AnnotationSandboxSchedulingHint is patched onto a pod by the koord-scheduler
	// when the pod cannot be scheduled. It carries structured diagnostic information
	// for agent orchestrators to use for self-healing (e.g. scale-out the warm pool,
	// reduce parallelism, or wait for capacity).
	//
	// The value is a JSON-encoded SandboxSchedulingHint.
	AnnotationSandboxSchedulingHint = "scheduling.koordinator.sh/sandbox-scheduling-hint"

	// AnnotationSandboxWarmPoolRef is set by the SandboxPipeline controller on pods
	// allocated from a kubernetes-sigs/agent-sandbox SandboxWarmPool. It enables the
	// scheduler to prefer nodes with pre-allocated Reservations for these pods.
	AnnotationSandboxWarmPoolRef = "scheduling.koordinator.sh/sandbox-warmpool-ref"
)

// SandboxRuntimeClass enumerates the agent sandbox runtimes supported by Koordinator.
// These values correspond to RuntimeClass names configured in the cluster.
type SandboxRuntimeClass string

const (
	// SandboxRuntimeGVisor is the gVisor (runsc) sandbox runtime.
	// Default QoS class: LS. The runsc process adds roughly 50MB of memory overhead
	// per sandbox instance, which makes LSR's exclusive CPU binding inappropriate
	// for general sandbox workloads.
	SandboxRuntimeGVisor SandboxRuntimeClass = "gvisor"

	// SandboxRuntimeKata is the Kata Containers sandbox runtime.
	// Default QoS class: LS. VM boot latency (around 500ms) is unsuitable for
	// LSR-tier SLAs but acceptable for latency-sensitive workloads.
	SandboxRuntimeKata SandboxRuntimeClass = "kata-containers"

	// SandboxRuntimeWasm is the WebAssembly sandbox runtime.
	// Default QoS class: BE. Wasm sandboxes are typically ephemeral sub-100ms
	// skill executions where best-effort scheduling is appropriate.
	SandboxRuntimeWasm SandboxRuntimeClass = "wasm"
)

// KnownSandboxRuntimeClass returns true if the given runtimeClassName corresponds
// to a known AI agent sandbox runtime supported by Koordinator.
func KnownSandboxRuntimeClass(runtimeClassName string) bool {
	switch SandboxRuntimeClass(runtimeClassName) {
	case SandboxRuntimeGVisor, SandboxRuntimeKata, SandboxRuntimeWasm:
		return true
	}
	return false
}

// DefaultQoSClassForSandboxRuntime returns the recommended Koordinator QoS class
// for a given agent sandbox runtime. The mapping reflects operational characteristics
// of each runtime documented in https://github.com/koordinator-sh/koordinator/issues/2879.
func DefaultQoSClassForSandboxRuntime(rc SandboxRuntimeClass) QoSClass {
	switch rc {
	case SandboxRuntimeGVisor, SandboxRuntimeKata:
		return QoSLS
	case SandboxRuntimeWasm:
		return QoSBE
	default:
		return QoSLS
	}
}

// SandboxSchedulingHint carries structured diagnostic information for agent orchestrators
// when a sandbox pod fails to schedule. It is JSON-encoded in AnnotationSandboxSchedulingHint.
type SandboxSchedulingHint struct {
	// Reason is a machine-readable failure code.
	// Known values: "warmPoolExhausted", "gpuMemoryInsufficient",
	// "nodeCapacityExceeded", "runtimeClassUnavailable"
	Reason string `json:"reason"`

	// NextStep is an action suggestion for the agent orchestrator.
	// Known values: "scale-out-warm-pool", "reduce-parallelism",
	// "wait-for-capacity", "use-different-node-pool"
	NextStep string `json:"nextStep"`

	// Runtime is the SandboxRuntimeClass that triggered this hint.
	Runtime SandboxRuntimeClass `json:"runtime,omitempty"`

	// SuggestedQoSClass is the QoS class the scheduler recommends for this runtime.
	// It is populated when the scheduling failure is caused by a QoS mismatch.
	SuggestedQoSClass QoSClass `json:"suggestedQoSClass,omitempty"`
}
