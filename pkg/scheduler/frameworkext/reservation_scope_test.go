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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestReservationInfoResourceScopeUpdates(t *testing.T) {
	cpuOnly := []corev1.ResourceName{corev1.ResourceCPU}
	both := []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}
	for _, path := range []string{"reservation", "operating-pod"} {
		for _, tc := range []struct {
			name                      string
			before, after             []corev1.ResourceName
			beforePolicy, afterPolicy schedulingv1alpha1.ReservationAllocatePolicy
			wantMemory                bool
		}{
			{"widen", cpuOnly, both, schedulingv1alpha1.ReservationAllocatePolicyRestricted, schedulingv1alpha1.ReservationAllocatePolicyRestricted, true},
			{"narrow", both, cpuOnly, schedulingv1alpha1.ReservationAllocatePolicyRestricted, schedulingv1alpha1.ReservationAllocatePolicyRestricted, false},
			{"unchanged", both, both, schedulingv1alpha1.ReservationAllocatePolicyRestricted, schedulingv1alpha1.ReservationAllocatePolicyRestricted, true},
			{"restricted-to-default", cpuOnly, cpuOnly, schedulingv1alpha1.ReservationAllocatePolicyRestricted, schedulingv1alpha1.ReservationAllocatePolicyDefault, true},
			{"default-to-restricted", cpuOnly, cpuOnly, schedulingv1alpha1.ReservationAllocatePolicyDefault, schedulingv1alpha1.ReservationAllocatePolicyRestricted, false},
		} {
			if path == "operating-pod" && tc.beforePolicy != tc.afterPolicy {
				continue
			}
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				template := corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"),
				}}}}}
				r := &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{Name: "r", UID: "r"},
					Spec:       schedulingv1alpha1.ReservationSpec{Template: &corev1.PodTemplateSpec{Spec: template}, AllocatePolicy: tc.beforePolicy},
				}
				p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "reserve", UID: "reserve"}, Spec: template}
				require.NoError(t, apiext.SetReservationRestrictedOptions(r, &apiext.ReservationRestrictedOptions{Resources: tc.before}))
				require.NoError(t, apiext.SetReservationRestrictedOptions(p, &apiext.ReservationRestrictedOptions{Resources: tc.before}))
				ri := NewReservationInfo(r)
				if path == "operating-pod" {
					ri = NewReservationInfoFromPod(p)
				}
				var owners []*corev1.Pod
				for _, uid := range []types.UID{"owner-a", "owner-b"} {
					owner := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: string(uid), UID: uid}, Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi"),
						}}}},
					}}
					owners = append(owners, owner)
					ri.AddAssignedPod(owner)
				}
				snapshot := ri.Clone()
				beforeMemory := snapshot.Allocated.Memory().Value()
				if path == "reservation" {
					updated := r.DeepCopy()
					updated.Spec.AllocatePolicy = tc.afterPolicy
					require.NoError(t, apiext.SetReservationRestrictedOptions(updated, &apiext.ReservationRestrictedOptions{Resources: tc.after}))
					ri.UpdateReservation(updated)
				} else {
					updated := p.DeepCopy()
					require.NoError(t, apiext.SetReservationRestrictedOptions(updated, &apiext.ReservationRestrictedOptions{Resources: tc.after}))
					ri.UpdatePod(updated)
				}
				wantMemory := int64(0)
				if tc.wantMemory {
					wantMemory = 4 << 30
				}
				assert.Equal(t, wantMemory, ri.Allocated.Memory().Value(), "allocated memory after scope update")
				assert.Equal(t, int64(2000), ri.Allocated.Cpu().MilliValue())
				assert.Equal(t, int64(8<<30)-wantMemory, ri.GetAvailable().GetMemory(), "available memory tracks allocation")
				assert.Equal(t, beforeMemory, snapshot.Allocated.Memory().Value(), "updating the live cache must not mutate a snapshot")
				ri.RemoveAssignedPod(owners[0])
				assert.Equal(t, wantMemory/2, ri.Allocated.Memory().Value(), "remaining owner's allocation")
				assert.Equal(t, int64(1000), ri.Allocated.Cpu().MilliValue())
			})
		}
	}
}
