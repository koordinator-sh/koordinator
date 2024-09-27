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

package mutating

import (
	"context"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func (h *PodMutatingHandler) deviceResourceSpecMutatingPod(ctx context.Context, req admission.Request, pod *corev1.Pod) error {
	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return nil
	}

	return h.mutateByDeviceResources(pod)
}

func (h *PodMutatingHandler) mutateByDeviceResources(pod *corev1.Pod) error {
	// device resource request euqal limit, not overcommit
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		r := getContainerExtendedResourcesRequirement(container, []corev1.ResourceName{
			extension.ResourceGPU,
			extension.ResourceGPUCore,
			extension.ResourceGPUMemory,
			extension.ResourceGPUMemoryRatio,
			extension.ResourceGPUShared,
		})
		if r == nil {
			continue
		}

		// use gpu api first
		_, ok := r.Requests[extension.ResourceGPU]
		if ok {
			injectGPU(container)
		}

		_, ok = r.Requests[extension.ResourceGPUShared]
		if !ok {
			injectGPUShare(container)
		}
	}

	klog.V(4).Infof("mutate Pod %s/%s by ExtendedResources", pod.Namespace, pod.Name)
	return nil
}

func injectResourceContainerSpec(c *corev1.Container, s *extension.ExtendedResourceContainerSpec) {
	for resource := range s.Requests {
		c.Resources.Requests[resource] = s.Requests[resource]
	}

	for resource := range s.Limits {
		c.Resources.Limits[resource] = s.Limits[resource]
	}
}

func injectGPUShare(c *corev1.Container) {
	containerSpec := &extension.ExtendedResourceContainerSpec{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}

	gpuMemoryRatioQuantity, gpuMemoryRatioExist := c.Resources.Requests[extension.ResourceGPUMemoryRatio]
	gpuCoreQuantity, gpuCoreExist := c.Resources.Requests[extension.ResourceGPUCore]

	gpuShared := int64(1)
	if gpuMemoryRatioExist {
		if gpuMemoryRatioQuantity.Value() > 100 && gpuMemoryRatioQuantity.Value()%100 == 0 {
			gpuShared = gpuMemoryRatioQuantity.Value() / 100
		}
	} else if gpuCoreExist {
		if gpuCoreQuantity.Value() > 100 && gpuCoreQuantity.Value()%100 == 0 {
			gpuShared = gpuMemoryRatioQuantity.Value() / 100
		}
	}

	containerSpec.Requests[extension.ResourceGPUShared] = *resource.NewQuantity(gpuShared, resource.DecimalSI)
	containerSpec.Limits[extension.ResourceGPUShared] = *resource.NewQuantity(gpuShared, resource.DecimalSI)

	injectResourceContainerSpec(c, containerSpec)
}

func injectGPU(c *corev1.Container) {
	gpuNumQuantity := c.Resources.Requests[extension.ResourceGPU]

	containerSpec := &extension.ExtendedResourceContainerSpec{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	containerSpec.Requests[extension.ResourceGPUCore] = *resource.NewQuantity(gpuNumQuantity.Value(), resource.DecimalSI)
	containerSpec.Limits[extension.ResourceGPUCore] = *resource.NewQuantity(gpuNumQuantity.Value(), resource.DecimalSI)
	containerSpec.Requests[extension.ResourceGPUMemoryRatio] = *resource.NewQuantity(gpuNumQuantity.Value(), resource.DecimalSI)
	containerSpec.Limits[extension.ResourceGPUMemoryRatio] = *resource.NewQuantity(gpuNumQuantity.Value(), resource.DecimalSI)

	injectResourceContainerSpec(c, containerSpec)

	delete(c.Resources.Requests, extension.ResourceGPU)
	delete(c.Resources.Limits, extension.ResourceGPU)
}
