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
	"k8s.io/apimachinery/pkg/types"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// Plugin must implement fwktype.SignPlugin so k8s 1.35 opportunistic
// batching does not fall back to disabled-for-all-pods. Anchor that
// expectation at package load.
var _ fwktype.SignPlugin = &Plugin{}

func TestPlugin_SignPod(t *testing.T) {
	suit := newPluginTestSuitWith(t, nil, nil)
	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	pl := p.(*Plugin)

	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: "default", UID: types.UID("n")},
	}
	reservePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rp", Namespace: "default", UID: types.UID("rp"),
			Annotations: map[string]string{
				reservationutil.AnnotationReservePod:      "true",
				reservationutil.AnnotationReservationName: "booked-r",
			},
		},
	}
	affinityPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ap", Namespace: "default", UID: types.UID("ap"),
			Annotations: map[string]string{
				apiext.AnnotationReservationAffinity: `{"reservationSelector":{"app":"demo"}}`,
			},
		},
	}

	// findFragment returns the value for the given key in the fragment slice
	// (or nil if missing). Tests use this to assert specific fragments
	// without coupling to the full fragment list.
	findFragment := func(fragments []fwktype.SignFragment, key string) any {
		for _, f := range fragments {
			if f.Key == key {
				return f.Value
			}
		}
		return nil
	}

	t.Run("reserve pod short-circuits to a single reservePodFor fragment", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), reservePod)
		assert.True(t, status == nil || status.IsSuccess(), "status should be nil or Success")
		require.Len(t, fragments, 1)
		assert.Equal(t, "koord.Reservation.reservePodFor", fragments[0].Key)
		assert.Equal(t, "booked-r", fragments[0].Value)
	})

	t.Run("plain pod always contributes the ownerInputs fragment", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), normalPod)
		assert.True(t, status == nil || status.IsSuccess())
		assert.NotNil(t, findFragment(fragments, "koord.Reservation.ownerInputs"),
			"every non-reserve pod must include owner-matching inputs")
		assert.Nil(t, findFragment(fragments, "koord.Reservation.affinity"))
	})

	t.Run("pod with reservation affinity emits both affinity and ownerInputs fragments", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), affinityPod)
		assert.True(t, status == nil || status.IsSuccess())
		assert.Equal(t, `{"reservationSelector":{"app":"demo"}}`,
			findFragment(fragments, "koord.Reservation.affinity"))
		assert.NotNil(t, findFragment(fragments, "koord.Reservation.ownerInputs"))
	})

	t.Run("reservation affinity canonicalization: same JSON shape produces the same affinity fragment", func(t *testing.T) {
		mk := func(name, anno string) *corev1.Pod {
			return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: name, UID: types.UID(name), Namespace: "default",
				Annotations: map[string]string{apiext.AnnotationReservationAffinity: anno},
			}}
		}
		a := mk("a", `{"reservationSelector":{"app":"demo"}}`)
		b := mk("b", "{ \"reservationSelector\" : { \"app\" : \"demo\" } }")
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		// Owner-input fragments differ (different pod identity) but the
		// affinity fragment must canonicalize to the same value.
		assert.Equal(t, findFragment(fa, "koord.Reservation.affinity"),
			findFragment(fb, "koord.Reservation.affinity"))
	})

	t.Run("malformed reservation affinity mirrors PreFilter and refuses to sign", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "bad", UID: "bad", Namespace: "default",
			Annotations: map[string]string{
				apiext.AnnotationReservationAffinity: "not-json",
			},
		}}
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status, "malformed affinity must not silently canonicalize into a fragment")
		assert.False(t, status.IsSuccess())
		assert.Nil(t, fragments)
	})

	t.Run("malformed exact-match-reservation mirrors PreFilter and refuses to sign", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "bad-exact", UID: "bad-exact", Namespace: "default",
			Annotations: map[string]string{
				apiext.AnnotationExactMatchReservationSpec: "not-json",
			},
		}}
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status, "malformed exact-match must not silently canonicalize into a fragment")
		assert.False(t, status.IsSuccess())
		assert.Nil(t, fragments)
	})

	t.Run("reservation-ignored label adds a dedicated fragment", func(t *testing.T) {
		base := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", UID: "p", Namespace: "default"}}
		ignored := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "ig", UID: "ig", Namespace: "default",
			Labels: map[string]string{apiext.LabelReservationIgnored: "true"},
		}}
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), ignored)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("exact-match-reservation annotation adds a dedicated fragment", func(t *testing.T) {
		base := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", UID: "p", Namespace: "default"}}
		exact := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "ex", UID: "ex", Namespace: "default",
			Annotations: map[string]string{
				apiext.AnnotationExactMatchReservationSpec: `{"resourceNames":["cpu"]}`,
			},
		}}
		fa, _ := pl.SignPod(context.TODO(), base)
		fb, _ := pl.SignPod(context.TODO(), exact)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("owner-matching inputs (labels and ownerReferences) influence the signature", func(t *testing.T) {
		a := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "a", UID: "a", Namespace: "default",
			Labels: map[string]string{"app": "x"},
		}}
		b := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "b", UID: "b", Namespace: "default",
			Labels: map[string]string{"app": "y"},
		}}
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("two pods with different identity always yield different signatures", func(t *testing.T) {
		// MatchObjectRef can match by pod UID/Name/Namespace, so two distinct
		// pods must not share a signature even when their labels and owners
		// are identical. KEP-5598 prefers correctness over batching here.
		mk := func(name string) *corev1.Pod {
			return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: name, UID: types.UID(name), Namespace: "default",
				Labels: map[string]string{"app": "z", "tier": "frontend"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "web",
				}},
			}}
		}
		fa, _ := pl.SignPod(context.TODO(), mk("a"))
		fb, _ := pl.SignPod(context.TODO(), mk("b"))
		assert.NotEqual(t, fa, fb)
	})

	t.Run("different namespace produces a different signature", func(t *testing.T) {
		mk := func(ns string) *corev1.Pod {
			return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", UID: "p", Namespace: ns,
			}}
		}
		fa, _ := pl.SignPod(context.TODO(), mk("ns-a"))
		fb, _ := pl.SignPod(context.TODO(), mk("ns-b"))
		assert.NotEqual(t, fa, fb,
			"MatchObjectRef and MatchReservationControllerReference both consult pod.Namespace")
	})

	t.Run("different ownerRef.UID produces a different signature", func(t *testing.T) {
		mk := func(name, uid string) *corev1.Pod {
			return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: name, UID: types.UID(name), Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "web",
					UID: types.UID(uid),
				}},
			}}
		}
		// Same pod identity, only ownerRef.UID differs.
		fa, _ := pl.SignPod(context.TODO(), mk("p", "owner-1"))
		fb, _ := pl.SignPod(context.TODO(), mk("p", "owner-2"))
		assert.NotEqual(t, fa, fb,
			"MatchReservationControllerReference matches by ownerRef.UID")
	})

	t.Run("different ownerRef.Controller flag produces a different signature", func(t *testing.T) {
		mkRef := func(controller *bool) metav1.OwnerReference {
			return metav1.OwnerReference{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "same",
				Controller: controller,
			}
		}
		t1, t2 := true, false
		mk := func(controller *bool) *corev1.Pod {
			return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", UID: "p", Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{mkRef(controller)},
			}}
		}
		fa, _ := pl.SignPod(context.TODO(), mk(&t1))
		fb, _ := pl.SignPod(context.TODO(), mk(&t2))
		fnil, _ := pl.SignPod(context.TODO(), mk(nil))
		assert.NotEqual(t, fa, fb)
		assert.NotEqual(t, fa, fnil)
		assert.NotEqual(t, fb, fnil)
	})

	mkPodWithRequests := func(name, cpu, mem string) *corev1.Pod {
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

	t.Run("aggregate pod requests influence the signature", func(t *testing.T) {
		fa, _ := pl.SignPod(context.TODO(), mkPodWithRequests("small", "1", "1Gi"))
		fb, _ := pl.SignPod(context.TODO(), mkPodWithRequests("large", "4", "8Gi"))
		assert.NotEqual(t, fa, fb,
			"reservation matching capacity-checks against pod requests; different requests must not share a signature")
	})

	t.Run("required node affinity produces a different signature", func(t *testing.T) {
		plain := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "plain", UID: "plain", Namespace: "default"}}
		withAff := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "aff", UID: "aff", Namespace: "default"},
			Spec: corev1.PodSpec{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{{
						MatchExpressions: []corev1.NodeSelectorRequirement{{
							Key:      "kubernetes.io/zone",
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{"a"},
						}},
					}},
				},
			}}},
		}
		fa, _ := pl.SignPod(context.TODO(), plain)
		fb, _ := pl.SignPod(context.TODO(), withAff)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("node selector produces a different signature", func(t *testing.T) {
		plain := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "plain", UID: "plain", Namespace: "default"}}
		withSel := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "sel", UID: "sel", Namespace: "default"},
			Spec:       corev1.PodSpec{NodeSelector: map[string]string{"zone": "a"}},
		}
		fa, _ := pl.SignPod(context.TODO(), plain)
		fb, _ := pl.SignPod(context.TODO(), withSel)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("required pod affinity flips the all-nodes pre-restore fragment", func(t *testing.T) {
		plain := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "plain", UID: "plain", Namespace: "default"}}
		withPodAff := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pa", UID: "pa", Namespace: "default"},
			Spec: corev1.PodSpec{Affinity: &corev1.Affinity{PodAffinity: &corev1.PodAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
					LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
					TopologyKey:   "kubernetes.io/hostname",
				}},
			}}},
		}
		fa, _ := pl.SignPod(context.TODO(), plain)
		fb, _ := pl.SignPod(context.TODO(), withPodAff)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("required topologySpreadConstraint flips the all-nodes pre-restore fragment", func(t *testing.T) {
		plain := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "plain", UID: "plain", Namespace: "default"}}
		withTSC := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "tsc", UID: "tsc", Namespace: "default"},
			Spec: corev1.PodSpec{TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
				MaxSkew:           1,
				TopologyKey:       "kubernetes.io/hostname",
				WhenUnsatisfiable: corev1.DoNotSchedule,
			}}},
		}
		fa, _ := pl.SignPod(context.TODO(), plain)
		fb, _ := pl.SignPod(context.TODO(), withTSC)
		assert.NotEqual(t, fa, fb)
	})
}
