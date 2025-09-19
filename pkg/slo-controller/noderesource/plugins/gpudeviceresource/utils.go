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
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

var (
	// ResourceNames are the known resources reconciled by the plugin from devices to nodes.
	// TODO: support custom resources.
	ResourceNames = []corev1.ResourceName{
		extension.ResourceGPU,
		extension.ResourceGPUCore,
		extension.ResourceHuaweiNPUCore,
		extension.ResourceHuaweiNPUCPU,
		extension.ResourceHuaweiNPUDVPP,
		extension.ResourceGPUMemory,
		extension.ResourceGPUMemoryRatio,
		extension.ResourceGPUShared,
		extension.ResourceNvidiaGPU,
	}
	// Labels are the known labels reconciled by the plugin from devices to nodes.
	Labels = []string{
		extension.LabelGPUVendor,
		extension.LabelGPUModel,
		extension.LabelGPUDriverVersion,
	}
)
