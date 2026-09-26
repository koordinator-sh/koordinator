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

package coscheduling

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	pgfake "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned/fake"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

var _ fwktype.SignPlugin = &Coscheduling{}

func TestCoscheduling_SignPod(t *testing.T) {
	pgClientSet := pgfake.NewSimpleClientset()
	cs := kubefake.NewSimpleClientset()
	suit := newPluginTestSuit(t, nil, pgClientSet, cs)

	p, err := suit.proxyNew(context.TODO(), suit.gangSchedulingArgs, suit.Handle)
	require.NoError(t, err)
	pl := p.(*Coscheduling)

	mkPod := func(name, ns string, labels map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: ns, UID: types.UID(name),
				Labels: labels,
			},
		}
	}

	t.Run("pod without PodGroup label contributes nothing", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", "default", nil))
		assert.True(t, status == nil || status.IsSuccess())
		assert.Empty(t, fragments)
	})

	t.Run("pod with PodGroup label contributes its gang id", func(t *testing.T) {
		labels := map[string]string{schedv1alpha1.PodGroupLabel: "gang-a"}
		fragments, status := pl.SignPod(context.TODO(), mkPod("p1", "ns-a", labels))
		assert.True(t, status == nil || status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "koord.Coscheduling.gang", fragments[0].Key)
	})

	t.Run("same gang yields identical fragments", func(t *testing.T) {
		labels := map[string]string{schedv1alpha1.PodGroupLabel: "gang-a"}
		fa, _ := pl.SignPod(context.TODO(), mkPod("p1", "ns-a", labels))
		fb, _ := pl.SignPod(context.TODO(), mkPod("p2", "ns-a", labels))
		assert.Equal(t, fa, fb)
	})

	t.Run("different gangs yield different fragments", func(t *testing.T) {
		fa, _ := pl.SignPod(context.TODO(), mkPod("p1", "ns-a", map[string]string{schedv1alpha1.PodGroupLabel: "gang-a"}))
		fb, _ := pl.SignPod(context.TODO(), mkPod("p2", "ns-a", map[string]string{schedv1alpha1.PodGroupLabel: "gang-b"}))
		assert.NotEqual(t, fa, fb)
	})

	t.Run("same gang name but different namespaces are distinct", func(t *testing.T) {
		labels := map[string]string{schedv1alpha1.PodGroupLabel: "gang-a"}
		fa, _ := pl.SignPod(context.TODO(), mkPod("p", "ns-a", labels))
		fb, _ := pl.SignPod(context.TODO(), mkPod("p", "ns-b", labels))
		assert.NotEqual(t, fa, fb)
	})

	// The selector drives PreScore/Score whether or not the pod belongs to a
	// gang, so the refusal must not sit behind the empty-gang short circuit.
	t.Run("network-topology selector without a gang is not signable", func(t *testing.T) {
		pod := mkPod("p-sel", "default", nil)
		pod.Annotations = map[string]string{
			extension.AnnotationPodNetworkTopologySelector: "topology-a",
		}
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.Unschedulable, status.Code())
		assert.Empty(t, fragments)
	})

	t.Run("network-topology selector with a gang is not signable", func(t *testing.T) {
		pod := mkPod("p-sel-gang", "ns-a", map[string]string{schedv1alpha1.PodGroupLabel: "gang-a"})
		pod.Annotations = map[string]string{
			extension.AnnotationPodNetworkTopologySelector: "topology-a",
		}
		fragments, status := pl.SignPod(context.TODO(), pod)
		require.NotNil(t, status)
		assert.Equal(t, fwktype.Unschedulable, status.Code())
		assert.Empty(t, fragments)
	})

	t.Run("empty selector annotation keeps the gang fragment", func(t *testing.T) {
		pod := mkPod("p-empty-sel", "ns-a", map[string]string{schedv1alpha1.PodGroupLabel: "gang-a"})
		pod.Annotations = map[string]string{
			extension.AnnotationPodNetworkTopologySelector: "",
		}
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status == nil || status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "koord.Coscheduling.gang", fragments[0].Key)
	})
}

// TestCoscheduling_SignPod_NetworkTopologyArgs covers the args-gated half of
// the signability boundary. SignPod reads nothing but cs.args and the pod, so
// the plugin is built directly rather than through the informer-backed suite.
func TestCoscheduling_SignPod_NetworkTopologyArgs(t *testing.T) {
	mkPod := func(name, ns string, labels map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: ns, UID: types.UID(name),
				Labels: labels,
			},
		}
	}
	gangLabels := map[string]string{schedv1alpha1.PodGroupLabel: "gang-a"}

	tests := []struct {
		name           string
		args           *config.CoschedulingArgs
		pod            *corev1.Pod
		wantUnsignable bool
		wantFragments  int
	}{
		{
			name:           "aware topology on, gang pod is not signable",
			args:           &config.CoschedulingArgs{AwareNetworkTopology: ptr.To(true)},
			pod:            mkPod("p", "ns-a", gangLabels),
			wantUnsignable: true,
		},
		{
			name:          "aware topology on, non-gang pod keeps ordinary behavior",
			args:          &config.CoschedulingArgs{AwareNetworkTopology: ptr.To(true)},
			pod:           mkPod("p", "ns-a", nil),
			wantFragments: 0,
		},
		{
			name:          "aware topology off, gang pod keeps its fragment",
			args:          &config.CoschedulingArgs{AwareNetworkTopology: ptr.To(false)},
			pod:           mkPod("p", "ns-a", gangLabels),
			wantFragments: 1,
		},
		{
			name:          "nil AwareNetworkTopology does not panic",
			args:          &config.CoschedulingArgs{},
			pod:           mkPod("p", "ns-a", gangLabels),
			wantFragments: 1,
		},
		{
			name:          "nil args does not panic",
			args:          nil,
			pod:           mkPod("p", "ns-a", gangLabels),
			wantFragments: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &Coscheduling{args: tt.args}
			fragments, status := cs.SignPod(context.TODO(), tt.pod)
			if tt.wantUnsignable {
				require.NotNil(t, status)
				assert.Equal(t, fwktype.Unschedulable, status.Code())
				assert.Empty(t, fragments)
				return
			}
			assert.True(t, status == nil || status.IsSuccess())
			assert.Len(t, fragments, tt.wantFragments)
		})
	}
}
