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

package defaultprebind

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	Name = "DefaultPreBind"
)

var _ framework.PreBindPlugin = &Plugin{}
var _ frameworkext.PreBindExtensions = &Plugin{}

type Plugin struct {
	clientSet      clientset.Interface
	koordClientSet koordinatorclientset.Interface
}

type koordClientSetHandle interface {
	KoordinatorClientSet() koordinatorclientset.Interface
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	koordClientSetHandle, _ := handle.(koordClientSetHandle)
	if koordClientSetHandle == nil {
		return nil, fmt.Errorf("framework.Handle cannot provide koordinator clientset")
	}
	return &Plugin{
		clientSet:      handle.ClientSet(),
		koordClientSet: koordClientSetHandle.KoordinatorClientSet(),
	}, nil
}

func (pl *Plugin) Name() string {
	return Name
}

func (pl *Plugin) PreBind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	return nil
}

func (pl *Plugin) ApplyPatch(ctx context.Context, cycleState *framework.CycleState, originalObj, modifiedObj metav1.Object) *framework.Status {
	if originalPod, ok := originalObj.(*corev1.Pod); ok {
		return pl.applyPodPatch(ctx, originalPod, modifiedObj.(*corev1.Pod))
	}

	if originalReservation, ok := originalObj.(*schedulingv1alpha1.Reservation); ok {
		return pl.applyReservationPatch(ctx, originalReservation, modifiedObj.(*schedulingv1alpha1.Reservation))
	}

	return nil
}

func (pl *Plugin) applyPodPatch(ctx context.Context, originalPod, modifiedPod *corev1.Pod) *framework.Status {
	var patchedPod *corev1.Pod
	err := util.RetryOnConflictOrTooManyRequests(func() error {
		got, err := util.PatchPodSafe(ctx, pl.clientSet, originalPod, modifiedPod)
		if err != nil {
			klog.ErrorS(err, "Failed to patch Pod", "pod", klog.KObj(originalPod), "uid", originalPod.UID)
			return err
		}
		patchedPod = got
		return nil
	})
	if err != nil {
		klog.ErrorS(err, "Failed to apply patch for Pod", "pod", klog.KObj(originalPod), "uid", originalPod.UID)
		return framework.AsStatus(err)
	}
	// NOTE: Patch might succeed for a deleting object without updating the data when the apiserver receive a Delete earlier.
	// In this case, we should clean up the reserved resources to avoid cache leak.
	if patchedPod.DeletionTimestamp != nil {
		err = fmt.Errorf("pod is being deleted")
		klog.ErrorS(err, "Failed to apply patch for Pod", "pod", klog.KObj(originalPod), "uid", originalPod.UID, "deletionTimestamp", patchedPod.DeletionTimestamp)
		return framework.AsStatus(err)
	}

	klog.V(4).InfoS("Successfully apply patch for Pod", "pod", klog.KObj(originalPod))
	return nil
}

func (pl *Plugin) applyReservationPatch(ctx context.Context, originalReservation, modifiedReservation *schedulingv1alpha1.Reservation) *framework.Status {
	var patchedReservation *schedulingv1alpha1.Reservation
	err := util.RetryOnConflictOrTooManyRequests(func() error {
		got, err := util.PatchReservationSafe(ctx, pl.koordClientSet, originalReservation, modifiedReservation)
		if err != nil {
			klog.ErrorS(err, "Failed to patch Reservation", "reservation", klog.KObj(originalReservation), "uid", originalReservation.UID)
			return err
		}
		patchedReservation = got
		return nil
	})
	if err != nil {
		klog.ErrorS(err, "Failed to apply patch for Reservation", "reservation", klog.KObj(originalReservation), "uid", originalReservation.UID)
		return framework.AsStatus(err)
	}
	// NOTE: Patch might succeed for a deleting object without updating the data when the apiserver receive a Delete earlier.
	// In this case, we should clean up the reserved resources to avoid cache leak.
	if patchedReservation.DeletionTimestamp != nil {
		err = fmt.Errorf("pod is being deleted")
		klog.ErrorS(err, "Failed to apply patch for Reservation", "reservation", klog.KObj(originalReservation), "uid", originalReservation.UID, "deletionTimestamp", patchedReservation.DeletionTimestamp)
		return framework.AsStatus(err)
	}

	klog.V(4).InfoS("Successfully apply patch for Reservation", "reservation", klog.KObj(originalReservation), "uid", originalReservation.UID)
	return nil
}
