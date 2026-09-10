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
	"k8s.io/utils/ptr"

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

	// mkPodWithRequests builds a non-zero-request pod. Tests that exercise
	// SignPod paths gated on PreFilter's !IsZero(requests) branch use this
	// helper so they reach the gated parses.
	mkPodWithRequests := func(name string, annos, labels map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", UID: types.UID(name),
				Annotations: annos,
				Labels:      labels,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "c",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				}},
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

	t.Run("malformed numa-topology-spec mirrors PreFilter Error status", func(t *testing.T) {
		pod := mkPod("bad", map[string]string{
			apiext.AnnotationNUMATopologySpec: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status, "malformed JSON must not silently canonicalize into a fragment")
		assert.Equal(t, fwktype.Error, status.Code())
		assert.Nil(t, fragments)
	})

	t.Run("malformed resource-spec mirrors PreFilter Error status", func(t *testing.T) {
		pod := mkPod("bad", map[string]string{
			apiext.AnnotationResourceSpec: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.Error, status.Code())
		assert.Nil(t, fragments)
	})

	t.Run("non-zero-request pod with reservation affinity emits hasReservationAffinity fragment", func(t *testing.T) {
		// Profiles that omit the Reservation plugin still need to
		// distinguish pods PreFilter would treat as reservation-bound,
		// so nodenumaresource emits its own presence bool.
		base := mkPodWithRequests("plain", nil, nil)
		withAff := mkPodWithRequests("with-aff", map[string]string{
			apiext.AnnotationReservationAffinity: `{"reservationSelector":{"app":"demo"}}`,
		}, nil)
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), withAff)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("malformed reservation affinity on non-zero-request pod returns UnschedulableAndUnresolvable", func(t *testing.T) {
		pod := mkPodWithRequests("bad-aff", map[string]string{
			apiext.AnnotationReservationAffinity: "not-json",
		}, nil)
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.UnschedulableAndUnresolvable, status.Code(),
			"malformed affinity must mirror PreFilter rather than silently sharing a signature")
		assert.Nil(t, fragments)
	})

	t.Run("zero-request pod with malformed reservation affinity is accepted (skip-gated)", func(t *testing.T) {
		// PreFilter returns Skip before reading reservation-affinity for
		// zero-request pods (plugin.go:354), so SignPod must NOT reject
		// such pods even if the annotation is malformed. Otherwise a
		// zero-request pod is opted out of batching that PreFilter would
		// happily skip, violating KEP-5598 parity.
		pod := mkPod("zero-bad-aff", map[string]string{
			apiext.AnnotationReservationAffinity: "not-json",
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status == nil || status.IsSuccess(),
			"zero-request pod must not be rejected on a skip-gated parse")
		assert.Empty(t, fragments)
	})

	t.Run("pre-allocation-required label adds a fragment for non-zero-request pods", func(t *testing.T) {
		base := mkPodWithRequests("plain", nil, nil)
		preAlloc := mkPodWithRequests("pre", nil, map[string]string{
			apiext.LabelPreAllocationRequired: "true",
		})
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), preAlloc)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("pre-allocation-required label is ignored for zero-request pods (skip-gated)", func(t *testing.T) {
		// PreFilter does not store isPreAllocationRequired for zero-request
		// pods, so signing the label here would diverge from PreFilter.
		base := mkPod("plain", nil)
		preAlloc := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pre", Namespace: "default", UID: "pre",
				Labels: map[string]string{apiext.LabelPreAllocationRequired: "true"},
			},
		}
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), preAlloc)
		assert.Equal(t, fa, fb, "zero-request pods must collapse regardless of pre-allocation-required label")
	})

	// AllowUseCPUSet gates the CPU-binding half of PreFilter, so pods that
	// differ in it are not interchangeable to this plugin. Whole CPUs are
	// used here rather than the 100m of mkPodWithRequests because that is the
	// shape a CPU-set pod actually has.
	mkCPUSetPod := func(name string, labels map[string]string, priority *int32) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", UID: types.UID(name),
				Labels: labels,
			},
			Spec: corev1.PodSpec{
				Priority: priority,
				Containers: []corev1.Container{{
					Name: "c",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
				}},
			},
		}
	}
	const allowUseCPUSetKey = "koord.NodeNUMAResource.allowUseCPUSet"
	lseLabels := map[string]string{apiext.LabelPodQoS: string(apiext.QoSLSE)}
	lsrLabels := map[string]string{apiext.LabelPodQoS: string(apiext.QoSLSR)}
	lsLabels := map[string]string{apiext.LabelPodQoS: string(apiext.QoSLS)}

	t.Run("cpu-set eligible pod is distinguishable from a non-eligible one", func(t *testing.T) {
		// LSE with no explicit priority defaults to koord-prod, so
		// AllowUseCPUSet is true; LS never is.
		eligible, status := pl.SignPod(context.TODO(), mkCPUSetPod("lse", lseLabels, nil))
		assert.True(t, status == nil || status.IsSuccess())
		notEligible, status := pl.SignPod(context.TODO(), mkCPUSetPod("ls", lsLabels, nil))
		assert.True(t, status == nil || status.IsSuccess())
		assert.NotEqual(t, eligible, notEligible)
		assert.Contains(t, fragmentKeys(eligible), allowUseCPUSetKey)
		assert.NotContains(t, fragmentKeys(notEligible), allowUseCPUSetKey)
	})

	t.Run("LSE and LSR produce the same eligibility fragment", func(t *testing.T) {
		// The signed value is the derived boolean, not the raw QoS label, so
		// two QoS classes that are equally CPU-set eligible are not split
		// into separate batches by this plugin.
		lse, _ := pl.SignPod(context.TODO(), mkCPUSetPod("lse", lseLabels, nil))
		lsr, _ := pl.SignPod(context.TODO(), mkCPUSetPod("lsr", lsrLabels, nil))
		assert.Equal(t, lse, lsr)
	})

	t.Run("identical labels differing only in spec.Priority are distinguishable", func(t *testing.T) {
		// This is the case no label-based signer can cover: both pods carry
		// exactly the same labels, so InterPodAffinity's v1.Pod.Labels
		// fragment is identical, yet the effective priority class - and with
		// it AllowUseCPUSet - differs.
		prod := mkCPUSetPod("prod", lseLabels, nil)
		batch := mkCPUSetPod("batch", lseLabels, ptr.To(apiext.PriorityBatchValueDefault))
		assert.Equal(t, prod.Labels, batch.Labels, "fixture must isolate spec.Priority")
		assert.True(t, AllowUseCPUSet(prod))
		assert.False(t, AllowUseCPUSet(batch))

		fa, _ := pl.SignPod(context.TODO(), prod)
		fb, _ := pl.SignPod(context.TODO(), batch)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("zero-request pods collapse regardless of cpu-set eligibility", func(t *testing.T) {
		// PreFilter Skips before reaching AllowUseCPUSet for zero-request
		// pods, so the fragment must stay inside the non-zero-request branch.
		lse := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "z-lse", Namespace: "default", UID: "z-lse", Labels: lseLabels,
		}}
		ls := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "z-ls", Namespace: "default", UID: "z-ls", Labels: lsLabels,
		}}
		fa, _ := pl.SignPod(context.TODO(), lse)
		fb, _ := pl.SignPod(context.TODO(), ls)
		assert.Equal(t, fa, fb)
		assert.Empty(t, fa)
	})
}

func fragmentKeys(fragments []fwktype.SignFragment) []string {
	keys := make([]string, 0, len(fragments))
	for _, f := range fragments {
		keys = append(keys, f.Key)
	}
	return keys
}
