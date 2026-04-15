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
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
)

var _ fwktype.SignPlugin = &Plugin{}

func TestPlugin_SignPod(t *testing.T) {
	// Disable the default-quota fallback so pods without a label, or with an
	// unknown quota label, yield no fragment. This keeps the test focused on
	// real signature differences.
	featuregatetesting.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.DisableDefaultQuota, true)

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(context.TODO(), suit.elasticQuotaArgs, suit.Handle)
	require.NoError(t, err)
	pl := p.(*Plugin)

	// Register two real quotas so the resolver (which cross-references a
	// tree map) can return their names.
	pl.addQuota("team-a", apiext.RootQuotaName, 96, 160, 10, 10, 96, 160, true, "", "tree-a")
	pl.addQuota("team-b", apiext.RootQuotaName, 96, 160, 10, 10, 96, 160, true, "", "tree-b")

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
		assert.True(t, status == nil || status.IsSuccess())
		assert.Empty(t, fragments)
	})

	t.Run("pod with tree id but no quota name contributes nothing", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", map[string]string{
			apiext.LabelQuotaTreeID: "tree-a",
		}))
		assert.True(t, status == nil || status.IsSuccess())
		assert.Empty(t, fragments)
	})

	t.Run("pod with known quota name contributes a fragment", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", map[string]string{
			apiext.LabelQuotaName: "team-a",
		}))
		assert.True(t, status == nil || status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "koord.ElasticQuota.quota", fragments[0].Key)
	})

	t.Run("two pods with the same quota label share the same fragment", func(t *testing.T) {
		labels := map[string]string{apiext.LabelQuotaName: "team-a"}
		fa, _ := pl.SignPod(context.TODO(), mkPod("a", labels))
		fb, _ := pl.SignPod(context.TODO(), mkPod("b", labels))
		assert.Equal(t, fa, fb)
	})

	t.Run("different quota names yield different fragments", func(t *testing.T) {
		fa, _ := pl.SignPod(context.TODO(), mkPod("a", map[string]string{apiext.LabelQuotaName: "team-a"}))
		fb, _ := pl.SignPod(context.TODO(), mkPod("b", map[string]string{apiext.LabelQuotaName: "team-b"}))
		assert.NotEqual(t, fa, fb)
	})
}
