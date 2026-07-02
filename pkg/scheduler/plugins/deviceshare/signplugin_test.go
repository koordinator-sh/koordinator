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

package deviceshare

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

// Plugin must satisfy fwktype.SignPlugin so opportunistic batching does
// not fall back to disabled globally.
var _ fwktype.SignPlugin = &Plugin{}

func TestPlugin_SignPod(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	mkPod := func(name string, requests corev1.ResourceList) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(name)},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:      "c",
					Resources: corev1.ResourceRequirements{Requests: requests},
				}},
			},
		}
	}

	t.Run("pod without device requests contributes nothing", func(t *testing.T) {
		pod := mkPod("plain", corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status == nil || status.IsSuccess())
		assert.Empty(t, fragments)
	})

	t.Run("pod requesting GPU contributes a fragment", func(t *testing.T) {
		pod := mkPod("gpu", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status == nil || status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "koord.DeviceShare.deviceRequests", fragments[0].Key)
		assert.NotNil(t, fragments[0].Value)
	})

	t.Run("identical GPU requests yield identical fragment values", func(t *testing.T) {
		a := mkPod("a", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("200")})
		b := mkPod("b", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("200")})
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		assert.Equal(t, fa, fb)
	})

	t.Run("different GPU amounts yield different fragment values", func(t *testing.T) {
		a := mkPod("a", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		b := mkPod("b", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("200")})
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("malformed device request opts the pod out of batching with the same status as PreFilter", func(t *testing.T) {
		// ValidatePercentageResource rejects values > 100 that are not
		// multiples of 100. 150 triggers that branch.
		pod := mkPod("bad", corev1.ResourceList{
			apiext.ResourceGPU: resource.MustParse("150"),
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status, "malformed input must yield an explicit Unschedulable status, not nil/Success")
		assert.False(t, status.IsSuccess(), "malformed requests should not produce a signature")
		assert.Equal(t, fwktype.UnschedulableAndUnresolvable, status.Code(),
			"status code must match preparePod so retry/preemption paths behave the same")
		assert.Nil(t, fragments)
	})

	mkPodWithAnno := func(name string, annos map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(name), Annotations: annos},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					apiext.ResourceGPU: resource.MustParse("100"),
				}},
			}}},
		}
	}

	t.Run("device-allocated annotation produces a different signature", func(t *testing.T) {
		base := mkPod("base", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		allocated := mkPodWithAnno("alloc", map[string]string{
			apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":0,"resources":{"koordinator.sh/gpu-core":"100"}}]}`,
		})
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, status := pl.SignPod(context.TODO(), allocated)
		assert.True(t, status == nil || status.IsSuccess())
		assert.NotEqual(t, fa, fb)
	})

	t.Run("device-allocate-hint annotation produces a different signature", func(t *testing.T) {
		base := mkPod("base", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		hinted := mkPodWithAnno("hint", map[string]string{
			apiext.AnnotationDeviceAllocateHint: `{"gpu":{"selector":{"matchLabels":{"a":"b"}}}}`,
		})
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), hinted)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("device-joint-allocate annotation produces a different signature", func(t *testing.T) {
		base := mkPod("base", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		joint := mkPodWithAnno("joint", map[string]string{
			apiext.AnnotationDeviceJointAllocate: `{"deviceTypes":["gpu","rdma"]}`,
		})
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), joint)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("gpu-partition-spec annotation produces a different signature", func(t *testing.T) {
		base := mkPod("base", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		part := mkPodWithAnno("part", map[string]string{
			apiext.AnnotationGPUPartitionSpec: `{"allocatePolicy":"Restricted"}`,
		})
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), part)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("malformed device-allocated annotation opts the pod out with UnschedulableAndUnresolvable", func(t *testing.T) {
		pod := mkPodWithAnno("bad-alloc", map[string]string{
			apiext.AnnotationDeviceAllocated: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.UnschedulableAndUnresolvable, status.Code(),
			"malformed JSON must not silently canonicalize into a fragment")
		assert.Nil(t, fragments)
	})

	t.Run("malformed device-allocate-hint annotation opts the pod out with UnschedulableAndUnresolvable", func(t *testing.T) {
		pod := mkPodWithAnno("bad-hint", map[string]string{
			apiext.AnnotationDeviceAllocateHint: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.UnschedulableAndUnresolvable, status.Code())
		assert.Nil(t, fragments)
	})

	t.Run("malformed device-joint-allocate annotation opts the pod out with UnschedulableAndUnresolvable", func(t *testing.T) {
		pod := mkPodWithAnno("bad-joint", map[string]string{
			apiext.AnnotationDeviceJointAllocate: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.UnschedulableAndUnresolvable, status.Code())
		assert.Nil(t, fragments)
	})

	t.Run("malformed gpu-partition-spec annotation opts the pod out with UnschedulableAndUnresolvable", func(t *testing.T) {
		pod := mkPodWithAnno("bad-part", map[string]string{
			apiext.AnnotationGPUPartitionSpec: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.UnschedulableAndUnresolvable, status.Code())
		assert.Nil(t, fragments)
	})

	// mkPodZeroRequest builds a pod that triggers preparePod's state.skip
	// branch (len(requests) == 0). The device-shape annotation parses
	// below this point are only performed when !state.skip, so SignPod
	// must short-circuit the same way.
	mkPodZeroRequest := func(name string, annos map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(name), Annotations: annos},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100m"),
				}},
			}}},
		}
	}

	t.Run("zero-request pod with malformed device-allocate-hint is accepted (skip-gated)", func(t *testing.T) {
		pod := mkPodZeroRequest("zero-bad-hint", map[string]string{
			apiext.AnnotationDeviceAllocateHint: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status == nil || status.IsSuccess(),
			"zero-request pod must not be rejected on a skip-gated parse")
		assert.Empty(t, fragments)
	})

	t.Run("zero-request pod with malformed device-joint-allocate is accepted (skip-gated)", func(t *testing.T) {
		pod := mkPodZeroRequest("zero-bad-joint", map[string]string{
			apiext.AnnotationDeviceJointAllocate: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status == nil || status.IsSuccess())
		assert.Empty(t, fragments)
	})

	t.Run("zero-request pod with malformed gpu-partition-spec is accepted (skip-gated)", func(t *testing.T) {
		pod := mkPodZeroRequest("zero-bad-part", map[string]string{
			apiext.AnnotationGPUPartitionSpec: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status == nil || status.IsSuccess())
		assert.Empty(t, fragments)
	})

	t.Run("zero-request pod with malformed device-allocated is still rejected (parsed unconditionally)", func(t *testing.T) {
		// preparePod parses GetDeviceAllocations before the state.skip
		// check, so SignPod must too. This lock-in test prevents a future
		// over-correction that would also gate this parse.
		pod := mkPodZeroRequest("zero-bad-alloc", map[string]string{
			apiext.AnnotationDeviceAllocated: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.UnschedulableAndUnresolvable, status.Code())
		assert.Nil(t, fragments)
	})

	t.Run("reservation affinity does not add a deviceshare fragment", func(t *testing.T) {
		// The Reservation plugin's SignPod already signs the affinity
		// annotation, so deviceshare deliberately does not emit a redundant
		// "hasReservationAffinity" fragment here.
		base := mkPod("base", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		withAff := mkPodWithAnno("aff", map[string]string{
			apiext.AnnotationReservationAffinity: `{"reservationSelector":{"app":"demo"}}`,
		})
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), withAff)
		assert.Equal(t, fa, fb, "affinity presence must not be re-signed here")
	})

	t.Run("pre-allocation-required label does not add a deviceshare fragment", func(t *testing.T) {
		// The Reservation plugin's SignPod owns the pre-allocation-required
		// fragment; deviceshare deliberately does not re-sign it.
		base := mkPod("base", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		preAlloc := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pre", Namespace: "default", UID: "pre",
				Labels: map[string]string{apiext.LabelPreAllocationRequired: "true"},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					apiext.ResourceGPU: resource.MustParse("100"),
				}},
			}}},
		}
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), preAlloc)
		assert.Equal(t, fa, fb, "pre-allocation-required must not be re-signed here")
	})
}
