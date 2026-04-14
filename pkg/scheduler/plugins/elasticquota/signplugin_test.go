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

package elasticquota

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
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(context.TODO(), suit.elasticQuotaArgs, suit.Handle)
	require.NoError(t, err)
	pl := p.(*Plugin)

	mkPod := func(name string, labels map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", UID: types.UID(name),
				Labels: labels,
			},
		}
	}

	t.Run("pod without quota labels contributes nothing", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", nil))
		assert.True(t, status.IsSuccess())
		assert.Empty(t, fragments)
	})

	t.Run("pod with quota name contributes a fragment", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", map[string]string{
			apiext.LabelQuotaName: "team-a",
		}))
		assert.True(t, status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "koord.ElasticQuota.quota", fragments[0].Key)
	})

	t.Run("same quota name and tree yield identical fragments", func(t *testing.T) {
		labels := map[string]string{
			apiext.LabelQuotaName:   "team-a",
			apiext.LabelQuotaTreeID: "tree-1",
		}
		fa, _ := pl.SignPod(context.TODO(), mkPod("a", labels))
		fb, _ := pl.SignPod(context.TODO(), mkPod("b", labels))
		assert.Equal(t, fa, fb)
	})

	t.Run("different quota names yield different fragments", func(t *testing.T) {
		fa, _ := pl.SignPod(context.TODO(), mkPod("a", map[string]string{apiext.LabelQuotaName: "team-a"}))
		fb, _ := pl.SignPod(context.TODO(), mkPod("b", map[string]string{apiext.LabelQuotaName: "team-b"}))
		assert.NotEqual(t, fa, fb)
	})

	t.Run("different tree ids under same quota yield different fragments", func(t *testing.T) {
		fa, _ := pl.SignPod(context.TODO(), mkPod("a", map[string]string{
			apiext.LabelQuotaName:   "team-a",
			apiext.LabelQuotaTreeID: "tree-1",
		}))
		fb, _ := pl.SignPod(context.TODO(), mkPod("b", map[string]string{
			apiext.LabelQuotaName:   "team-a",
			apiext.LabelQuotaTreeID: "tree-2",
		}))
		assert.NotEqual(t, fa, fb)
	})
}
