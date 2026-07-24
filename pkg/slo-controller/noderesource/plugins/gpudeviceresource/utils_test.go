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

package gpudeviceresource

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestRegisterGPUDeviceResource(t *testing.T) {
	orig := make([]corev1.ResourceName, len(ResourceNames))
	copy(orig, ResourceNames)
	defer func() { ResourceNames = orig }()

	RegisterGPUDeviceResource("custom.resource/a")
	if len(ResourceNames) != len(orig)+1 {
		t.Fatalf("expected length %d, got %d", len(orig)+1, len(ResourceNames))
	}
	if ResourceNames[len(ResourceNames)-1] != "custom.resource/a" {
		t.Fatalf("expected last custom.resource/a, got %s", ResourceNames[len(ResourceNames)-1])
	}

	// register again
	RegisterGPUDeviceResource("custom.resource/a")
	if len(ResourceNames) != len(orig)+1 {
		t.Fatalf("duplicated registration, expected length %d, got %d", len(orig)+1, len(ResourceNames))
	}
}

func TestRegisterGPUDeviceLabel(t *testing.T) {
	orig := make([]string, len(Labels))
	copy(orig, Labels)
	defer func() { Labels = orig }()

	RegisterGPUDeviceLabel("custom.label/a")
	if len(Labels) != len(orig)+1 {
		t.Fatalf("expected length %d, got %d", len(orig)+1, len(Labels))
	}
	if Labels[len(Labels)-1] != "custom.label/a" {
		t.Fatalf("expected last custom.label/a, got %s", Labels[len(Labels)-1])
	}

	// register again
	RegisterGPUDeviceLabel("custom.label/a")
	if len(Labels) != len(orig)+1 {
		t.Fatalf("duplicated registration, expected length %d, got %d", len(orig)+1, len(Labels))
	}
}
