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

package nodenumaresource

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

var _ fwktype.SignPlugin = &Plugin{}

func TestPlugin_SignPod(t *testing.T) {
	suit := newPluginTestSuit(t, nil, nil)
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	require.NoError(t, err)
	pl := p.(*Plugin)

	mkPod := func(name string, annos map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", UID: types.UID(name),
				Annotations: annos,
			},
		}
	}

	t.Run("pod without NUMA annotations contributes nothing", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", nil))
		assert.True(t, status == nil || status.IsSuccess())
		assert.Empty(t, fragments)
	})

	t.Run("pod with NUMA topology spec contributes one fragment", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", map[string]string{
			apiext.AnnotationNUMATopologySpec: `{"numaTopologyPolicy":"SingleNUMANode"}`,
		}))
		assert.True(t, status == nil || status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "koord.NodeNUMAResource.numaTopology", fragments[0].Key)
	})

	t.Run("same NUMA annotations produce identical fragments", func(t *testing.T) {
		a := mkPod("a", map[string]string{
			apiext.AnnotationNUMATopologySpec: `{"numaTopologyPolicy":"SingleNUMANode"}`,
		})
		b := mkPod("b", map[string]string{
			apiext.AnnotationNUMATopologySpec: `{"numaTopologyPolicy":"SingleNUMANode"}`,
		})
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		assert.Equal(t, fa, fb)
	})

	t.Run("different NUMA policies produce different fragments", func(t *testing.T) {
		a := mkPod("a", map[string]string{
			apiext.AnnotationNUMATopologySpec: `{"numaTopologyPolicy":"SingleNUMANode"}`,
		})
		b := mkPod("b", map[string]string{
			apiext.AnnotationNUMATopologySpec: `{"numaTopologyPolicy":"BestEffort"}`,
		})
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("resource-spec annotation also contributes", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", map[string]string{
			apiext.AnnotationResourceSpec: `{"preferredCPUBindPolicy":"FullPCPUs"}`,
		}))
		assert.True(t, status == nil || status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "koord.NodeNUMAResource.resourceSpec", fragments[0].Key)
	})

	t.Run("identical annotations with different whitespace share the same fragment", func(t *testing.T) {
		a := mkPod("a", map[string]string{
			apiext.AnnotationNUMATopologySpec: `{"numaTopologyPolicy":"SingleNUMANode"}`,
		})
		b := mkPod("b", map[string]string{
			apiext.AnnotationNUMATopologySpec: "{ \"numaTopologyPolicy\" : \"SingleNUMANode\" }",
		})
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		assert.Equal(t, fa, fb)
	})

	t.Run("malformed JSON annotation falls back to raw value", func(t *testing.T) {
		pod := mkPod("bad", map[string]string{
			apiext.AnnotationNUMATopologySpec: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status == nil || status.IsSuccess())
		// The numaTopology fragment falls back, no extra fragments since the
		// pod has no requests/affinity/pre-allocation labels.
		require.Len(t, fragments, 1)
		assert.Equal(t, "koord.NodeNUMAResource.numaTopology", fragments[0].Key)
		assert.Equal(t, "not-json", fragments[0].Value)
	})

	mkPodWithRequests := func(name string, cpu, mem string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(name)},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(cpu),
					corev1.ResourceMemory: resource.MustParse(mem),
				}},
			}}},
		}
	}

	t.Run("different aggregate requests yield different signatures", func(t *testing.T) {
		fa, _ := pl.SignPod(context.TODO(), mkPodWithRequests("small", "1", "1Gi"))
		fb, _ := pl.SignPod(context.TODO(), mkPodWithRequests("large", "4", "8Gi"))
		assert.NotEqual(t, fa, fb)
	})

	t.Run("identical requests share a request fragment", func(t *testing.T) {
		fa, _ := pl.SignPod(context.TODO(), mkPodWithRequests("a", "2", "4Gi"))
		fb, _ := pl.SignPod(context.TODO(), mkPodWithRequests("b", "2", "4Gi"))
		assert.Equal(t, fa, fb)
	})

	t.Run("reservation affinity adds a dedicated fragment", func(t *testing.T) {
		base := mkPod("plain", nil)
		withAff := mkPod("with-aff", map[string]string{
			apiext.AnnotationReservationAffinity: `{"reservationSelector":{"app":"demo"}}`,
		})
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), withAff)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("malformed reservation affinity returns UnschedulableAndUnresolvable", func(t *testing.T) {
		pod := mkPod("bad-aff", map[string]string{
			apiext.AnnotationReservationAffinity: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.UnschedulableAndUnresolvable, status.Code(),
			"malformed affinity must mirror PreFilter rather than silently sharing a signature")
		assert.Nil(t, fragments)
	})

	t.Run("pre-allocation-required label adds a dedicated fragment", func(t *testing.T) {
		base := mkPod("plain", nil)
		preAlloc := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pre", Namespace: "default", UID: "pre",
				Labels: map[string]string{apiext.LabelPreAllocationRequired: "true"},
			},
		}
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), preAlloc)
		assert.NotEqual(t, fa, fb)
	})
}
