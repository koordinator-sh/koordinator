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
	"encoding/json"
	"net/http"
	"reflect"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// PodMutatingHandler handles Pod
type PodMutatingHandler struct {
	Client client.Client

	// Decoder decodes objects
	Decoder *admission.Decoder
}

var _ admission.Handler = &PodMutatingHandler{}

func shouldIgnoreIfNotPod(req admission.Request) bool {
	// Ignore all calls to sub resources or resources other than pods.
	if len(req.AdmissionRequest.SubResource) != 0 ||
		req.AdmissionRequest.Resource.Resource != "pods" {
		return true
	}
	return false
}

// Handle handles admission requests.
func (h *PodMutatingHandler) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	if shouldIgnoreIfNotPod(req) {
		return admission.Allowed("")
	}

	obj := &corev1.Pod{}
	err := h.Decoder.Decode(req, obj)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	clone := obj.DeepCopy()
	// when pod.namespace is empty, using req.namespace
	var isNamespaceEmpty bool
	if obj.Namespace == "" {
		obj.Namespace = req.Namespace
		isNamespaceEmpty = true
	}

	switch req.Operation {
	case admissionv1.Create:
		err = h.handleCreate(ctx, req, obj)
	case admissionv1.Update:
		err = h.handleUpdate(ctx, req, obj)
	default:
		return admission.Allowed("")
	}

	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	// Do not modify namespace in webhook
	if isNamespaceEmpty {
		obj.Namespace = ""
	}

	if reflect.DeepEqual(obj, clone) {
		return admission.Allowed("")
	}
	marshaled, err := json.Marshal(obj)
	if err != nil {
		klog.Errorf("Failed to marshal mutated Pod %s/%s, err: %v", obj.Namespace, obj.Name, err)
		return admission.Errored(http.StatusInternalServerError, err)
	}
	original, err := json.Marshal(clone)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(original, marshaled)
}

func (h *PodMutatingHandler) handleCreate(ctx context.Context, req admission.Request, obj *corev1.Pod) error {
	if err := h.clusterColocationProfileMutatingPod(ctx, req, obj); err != nil {
		klog.Errorf("Failed to mutating Pod %s/%s by ClusterColocationProfile, err: %v", obj.Namespace, obj.Name, err)
		return err
	}

	if err := h.extendedResourceSpecMutatingPod(ctx, req, obj); err != nil {
		klog.Errorf("Failed to mutating Pod %s/%s by ExtendedResourceSpec, err: %v", obj.Namespace, obj.Name, err)
		return err
	}

	if err := h.addNodeAffinityForMultiQuotaTree(ctx, req, obj); err != nil {
		klog.Errorf("Failed to mutating Pod %s/%s by MultiQuotaTree, err: %v", obj.Namespace, obj.Name, err)
		return err
	}

	if err := h.deviceResourceSpecMutatingPod(ctx, req, obj); err != nil {
		klog.Errorf("Failed to mutating Pod %s/%s by DeviceResourceSpec, err: %v", obj.Namespace, obj.Name, err)
		return err
	}

	return nil
}

func (h *PodMutatingHandler) handleUpdate(ctx context.Context, req admission.Request, obj *corev1.Pod) error {
	// TODO: add mutating logic for pod update here
	return nil
}

// var _ inject.Client = &PodMutatingHandler{}

// InjectClient injects the client into the PodMutatingHandler
func (h *PodMutatingHandler) InjectClient(c client.Client) error {
	h.Client = c
	return nil
}

// var _ admission.DecoderInjector = &PodMutatingHandler{}

// InjectDecoder injects the decoder into the PodMutatingHandler
func (h *PodMutatingHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}
