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
		assert.Len(t, fragments, 1)
		assert.Equal(t, "not-json", fragments[0].Value)
	})
}
