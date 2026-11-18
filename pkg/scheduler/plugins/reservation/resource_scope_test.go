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

package reservation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// The fixture has no other reservation filter plugins or background controllers.
type scopeUpdateFilterHandle struct {
	frameworkext.FrameworkExtender
}

func (*scopeUpdateFilterHandle) RunReservationFilterPlugins(context.Context, fwktype.CycleState, *corev1.Pod, *frameworkext.ReservationInfo, fwktype.NodeInfo) *fwktype.Status {
	return nil
}

func TestFilterAfterReservationResourceScopeWidening(t *testing.T) {
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r", UID: "r"},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			AllocateOnce:   ptr.To(false),
			Template: &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"),
			}}}}}},
		},
		Status: schedulingv1alpha1.ReservationStatus{NodeName: "node", Phase: schedulingv1alpha1.ReservationAvailable, Allocatable: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"),
		}},
	}
	require.NoError(t, apiext.SetReservationRestrictedOptions(r, &apiext.ReservationRestrictedOptions{Resources: []corev1.ResourceName{corev1.ResourceCPU}}))
	owner := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "owner", UID: "owner"}, Spec: corev1.PodSpec{NodeName: "node",
		Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("4Gi"),
		}}}},
	}}
	cache := newReservationCache(nil)
	cache.updateReservation(r)
	require.NoError(t, cache.addPod(r.UID, owner))
	updated := r.DeepCopy()
	require.NoError(t, apiext.SetReservationRestrictedOptions(updated, &apiext.ReservationRestrictedOptions{Resources: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}}))
	cache.updateReservation(updated)
	ri := cache.getReservationInfoByUID(r.UID)
	require.NotNil(t, ri)
	require.ElementsMatch(t, []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}, ri.ResourceNames)
	require.Equal(t, int64(8<<30), ri.Allocatable.Memory().Value())
	require.Len(t, ri.AssignedPods, 1)

	node := framework.NewNodeInfo(reservationutil.NewReservePod(updated), owner)
	node.SetNode(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node"}, Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("32"), corev1.ResourceMemory: resource.MustParse("64Gi"), corev1.ResourcePods: resource.MustParse("100"),
	}}})
	pl := &Plugin{handle: &scopeUpdateFilterHandle{}}
	for _, tc := range []struct {
		memory string
		want   fwktype.Code
	}{
		{"4Gi", fwktype.Success},
		{"5Gi", fwktype.Unschedulable},
	} {
		t.Run(tc.memory, func(t *testing.T) {
			requests := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse(tc.memory)}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "waiter", UID: "waiter"}, Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: requests}}},
			}}
			state := &stateData{schedulingStateData: schedulingStateData{
				hasAffinity:          true,
				podRequests:          requests,
				podRequestsResources: framework.NewResource(requests),
				podResourceNames:     quotav1.ResourceNames(requests),
				nodeReservationStates: map[string]*nodeReservationState{"node": {
					matchedOrIgnored: []*frameworkext.ReservationInfo{ri},
					podRequested:     node.GetRequested(),
					rAllocated:       framework.NewResource(ri.Allocated),
				}},
			}}
			cycle := framework.NewCycleState()
			cycle.Write(stateKey, state)
			status := pl.Filter(context.Background(), cycle, pod, node)
			t.Logf("allocatedMemory=%s requestedMemory=%s filter=%v", ri.Allocated.Memory().String(), tc.memory, status)
			assert.Equal(t, tc.want, status.Code())
			if tc.want == fwktype.Unschedulable {
				assert.Contains(t, status.Message(), "Insufficient memory")
			}
		})
	}
}
