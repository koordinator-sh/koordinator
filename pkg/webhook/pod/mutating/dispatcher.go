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
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	LabelWorkloadType            = "workload-type"
	WorkloadTypeAI               = "ai"
	WorkloadTypeLatencySensitive = "latency-sensitive"
	WorkloadTypeBatch            = "batch"

	SchedulerGPUShare    = "koordinator-gpu-scheduler"
	SchedulerLatencySens = "koordinator-ls-scheduler"
	SchedulerBatch       = "koordinator-batch-scheduler"
	SchedulerDefault     = "koord-scheduler"
)

func (h *PodMutatingHandler) multiSchedulerDispatchMutatingPod(ctx context.Context, req admission.Request, pod *corev1.Pod) error {
	if req.Operation != admissionv1.Create {
		return nil
	}

	workloadType := ""
	if pod.Labels != nil {
		workloadType = pod.Labels[LabelWorkloadType]
	}

	// If user explicitly specified a custom scheduler name (not default-scheduler and not empty), do NOT overwrite it.
	if pod.Spec.SchedulerName != "" && pod.Spec.SchedulerName != "default-scheduler" && pod.Spec.SchedulerName != SchedulerDefault {
		return nil
	}

	// 1. Detect AI/GPU workload
	isGPUWorkload := false
	for _, container := range pod.Spec.Containers {
		if _, requestsGPU := container.Resources.Requests[corev1.ResourceName("koordinator.sh/gpu")]; requestsGPU {
			isGPUWorkload = true
			break
		}
		if _, requestsNvidia := container.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]; requestsNvidia {
			isGPUWorkload = true
			break
		}
	}

	if workloadType == WorkloadTypeAI || isGPUWorkload {
		pod.Spec.SchedulerName = SchedulerGPUShare
		klog.V(4).Infof("Multi-Scheduler Dispatcher: routed pod %s/%s to %s", pod.Namespace, pod.Name, SchedulerGPUShare)
		return nil
	}

	// 2. Detect Latency-Sensitive workload (LSR/LSE CPUSet QoS)
	isLSWorkload := false
	qosClass := extension.GetPodQoSClassRaw(pod)
	if qosClass == extension.QoSLSR || qosClass == extension.QoSLSE {
		isLSWorkload = true
	}

	if workloadType == WorkloadTypeLatencySensitive || isLSWorkload {
		pod.Spec.SchedulerName = SchedulerLatencySens
		klog.V(4).Infof("Multi-Scheduler Dispatcher: routed pod %s/%s to %s", pod.Namespace, pod.Name, SchedulerLatencySens)
		return nil
	}

	// 3. Detect Batch workload (BE QoS or Batch priority class)
	isBatchWorkload := false
	if qosClass == extension.QoSBE {
		isBatchWorkload = true
	}

	if workloadType == WorkloadTypeBatch || isBatchWorkload {
		pod.Spec.SchedulerName = SchedulerBatch
		klog.V(4).Infof("Multi-Scheduler Dispatcher: routed pod %s/%s to %s", pod.Namespace, pod.Name, SchedulerBatch)
		return nil
	}

	// Default scheduler name assignment
	if pod.Spec.SchedulerName == "" || pod.Spec.SchedulerName == "default-scheduler" {
		pod.Spec.SchedulerName = SchedulerDefault
		klog.V(4).Infof("Multi-Scheduler Dispatcher: assigned default scheduler %s to pod %s/%s", SchedulerDefault, pod.Namespace, pod.Name)
	}

	return nil
}
