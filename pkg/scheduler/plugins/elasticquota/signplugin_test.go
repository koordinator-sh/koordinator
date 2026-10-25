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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
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

	// PreFilter compares core.PodRequests(pod) against the quota's runtime, but
	// it runs for every pod before a hint is fetched, so the requests do not
	// have to split the batch.
	t.Run("pods differing only in requests share the quota fragment", func(t *testing.T) {
		withCPU := func(name, cpu string) *corev1.Pod {
			pod := mkPod(name, map[string]string{apiext.LabelQuotaName: "team-a"})
			pod.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)},
			}}}
			return pod
		}
		small, big := withCPU("small", "1"), withCPU("big", "64")
		require.NotEqual(t, core.PodRequests(small), core.PodRequests(big))

		fa, _ := pl.SignPod(context.TODO(), small)
		fb, _ := pl.SignPod(context.TODO(), big)
		assert.Equal(t, fa, fb)
	})

	// Same for the non-preemptible branch, which only tightens PreFilter's
	// check from the quota's runtime to its min.
	t.Run("pods differing only in preemptibility share the quota fragment", func(t *testing.T) {
		preemptible := mkPod("a", map[string]string{apiext.LabelQuotaName: "team-a"})
		nonPreemptible := mkPod("b", map[string]string{
			apiext.LabelQuotaName:   "team-a",
			apiext.LabelPreemptible: "false",
		})
		require.False(t, apiext.IsPodNonPreemptible(preemptible))
		require.True(t, apiext.IsPodNonPreemptible(nonPreemptible))

		fa, _ := pl.SignPod(context.TODO(), preemptible)
		fb, _ := pl.SignPod(context.TODO(), nonPreemptible)
		assert.Equal(t, fa, fb)
	})
}

// The two sub-tests above hold only while PreFilter is this plugin's sole
// batchable phase: Filter and Score results are what a batch hint reuses.
// Adding either means SignPod has to sign whatever the new phase reads.
func TestPlugin_SignPodScopeStaysPreFilterOnly(t *testing.T) {
	var pl any = &Plugin{}
	_, isPreFilter := pl.(fwktype.PreFilterPlugin)
	_, isFilter := pl.(fwktype.FilterPlugin)
	_, isPreScore := pl.(fwktype.PreScorePlugin)
	_, isScore := pl.(fwktype.ScorePlugin)
	assert.True(t, isPreFilter)
	assert.False(t, isFilter, "Filter added, SignPod must sign what it reads")
	assert.False(t, isPreScore, "PreScore added, SignPod must sign what it reads")
	assert.False(t, isScore, "Score added, SignPod must sign what it reads")
}
