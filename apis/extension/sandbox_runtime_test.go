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
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

func TestKnownSandboxRuntimeClass(t *testing.T) {
	tests := []struct {
		name             string
		runtimeClassName string
		want             bool
	}{
		{name: "gvisor is known", runtimeClassName: "gvisor", want: true},
		{name: "kata-containers is known", runtimeClassName: "kata-containers", want: true},
		{name: "wasm is known", runtimeClassName: "wasm", want: true},
		{name: "runc is not a sandbox runtime", runtimeClassName: "runc", want: false},
		{name: "empty string is not known", runtimeClassName: "", want: false},
		{name: "partial match does not count", runtimeClassName: "gvisor-extra", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := KnownSandboxRuntimeClass(tt.runtimeClassName)
			if got != tt.want {
				t.Errorf("KnownSandboxRuntimeClass(%q) = %v, want %v",
					tt.runtimeClassName, got, tt.want)
			}
		})
	}
}

func TestDefaultQoSClassForSandboxRuntime(t *testing.T) {
	tests := []struct {
		name string
		rc   SandboxRuntimeClass
		want QoSClass
	}{
		{
			name: "gvisor maps to LS due to runsc overhead",
			rc:   SandboxRuntimeGVisor,
			want: QoSLS,
		},
		{
			name: "kata maps to LS due to VM boot latency",
			rc:   SandboxRuntimeKata,
			want: QoSLS,
		},
		{
			name: "wasm maps to BE for ephemeral skill execution",
			rc:   SandboxRuntimeWasm,
			want: QoSBE,
		},
		{
			name: "unknown runtime defaults to LS",
			rc:   SandboxRuntimeClass("unknown"),
			want: QoSLS,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultQoSClassForSandboxRuntime(tt.rc)
			if got != tt.want {
				t.Errorf("DefaultQoSClassForSandboxRuntime(%q) = %v, want %v",
					tt.rc, got, tt.want)
			}
		})
	}
}

func TestSandboxSchedulingHintConstants(t *testing.T) {
	// Verify annotation keys are in the expected domain
	keys := []string{
		LabelSandboxRuntimeClass,
		AnnotationSandboxPipelineName,
		AnnotationSandboxSchedulingHint,
		AnnotationSandboxWarmPoolRef,
	}
	for _, k := range keys {
		if len(k) == 0 {
			t.Errorf("annotation/label constant must not be empty")
		}
	}
}

func TestDefaultRuntimeOverheadForSandbox(t *testing.T) {
	tests := []struct {
		name       string
		rc         SandboxRuntimeClass
		wantMemory string
		wantCPU    string
	}{
		{
			name:       "gVisor overhead reflects runsc supervisor cost",
			rc:         SandboxRuntimeGVisor,
			wantMemory: "50Mi",
			wantCPU:    "50m",
		},
		{
			name:       "Kata overhead reflects VM kernel and agent cost",
			rc:         SandboxRuntimeKata,
			wantMemory: "128Mi",
			wantCPU:    "100m",
		},
		{
			name:       "Wasm overhead is minimal for ephemeral skill executions",
			rc:         SandboxRuntimeWasm,
			wantMemory: "16Mi",
			wantCPU:    "10m",
		},
		{
			name:       "unknown runtime falls back to gVisor defaults",
			rc:         SandboxRuntimeClass("unknown-runtime"),
			wantMemory: "50Mi",
			wantCPU:    "50m",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultRuntimeOverheadForSandbox(tt.rc)
			wantMem := resource.MustParse(tt.wantMemory)
			wantCPU := resource.MustParse(tt.wantCPU)
			if got.Memory.Cmp(wantMem) != 0 {
				t.Errorf("Memory = %v, want %v", got.Memory.String(), tt.wantMemory)
			}
			if got.CPU.Cmp(wantCPU) != 0 {
				t.Errorf("CPU = %v, want %v", got.CPU.String(), tt.wantCPU)
			}
		})
	}
}
