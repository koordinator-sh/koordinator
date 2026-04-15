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
	corev1 "k8s.io/api/core/v1"
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

	type kv struct {
		key string
		val any
	}
	toKVs := func(fragments []fwktype.SignFragment) []kv {
		out := make([]kv, 0, len(fragments))
		for _, f := range fragments {
			out = append(out, kv{key: f.Key, val: f.Value})
		}
		return out
	}

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

	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected []kv
	}{
		{
			name:     "plain pod contributes no fragments",
			pod:      normalPod,
			expected: []kv{},
		},
		{
			name: "reserve pod contributes the target reservation name",
			pod:  reservePod,
			expected: []kv{
				{key: "koord.Reservation.reservePodFor", val: "booked-r"},
			},
		},
		{
			name: "pod with reservation affinity contributes the raw affinity",
			pod:  affinityPod,
			expected: []kv{
				{key: "koord.Reservation.affinity", val: `{"reservationSelector":{"app":"demo"}}`},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fragments, status := pl.SignPod(context.TODO(), tt.pod)
			assert.True(t, status == nil || status.IsSuccess(), "status should be nil or Success")
			assert.Equal(t, tt.expected, toKVs(fragments))
		})
	}

	t.Run("reservation affinity with different formatting produces the same fragment", func(t *testing.T) {
		a := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "a", UID: "a", Namespace: "default",
			Annotations: map[string]string{
				apiext.AnnotationReservationAffinity: `{"reservationSelector":{"app":"demo"}}`,
			},
		}}
		b := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "b", UID: "b", Namespace: "default",
			Annotations: map[string]string{
				// Identical meaning but with whitespace around the object.
				apiext.AnnotationReservationAffinity: "{ \"reservationSelector\" : { \"app\" : \"demo\" } }",
			},
		}}
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		assert.Equal(t, fa, fb)
	})

	t.Run("malformed reservation affinity falls back to the raw string", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "bad", UID: "bad", Namespace: "default",
			Annotations: map[string]string{
				apiext.AnnotationReservationAffinity: "not-json",
			},
		}}
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status == nil || status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "not-json", fragments[0].Value)
	})
}
