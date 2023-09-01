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
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func createProfileWithNodeSelector(name string, selectorLabels map[string]string) *v1alpha1.ElasticQuotaProfile {
	return &v1alpha1.ElasticQuotaProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ElasticQuotaProfileSpec{
			QuotaName: name,
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
		},
	}
}

func TestPlugin_QuotaProfileFunction(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.MultiQuotaTree, true)()

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	// add node
	nodes := []*corev1.Node{
		defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
		defaultCreateNodeWithLabels("node2", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
		defaultCreateNodeWithLabels("node3", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-b"}),
	}
	for _, node := range nodes {
		plugin.handle.ClientSet().CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
	}
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, plugin.groupQuotaManager.GetClusterTotalResource(), createResourceList(300, 3000))

	// add profile  tests
	profile := createProfileWithNodeSelector("profile", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"})
	updatedProfile, err := plugin.koordClient.QuotaV1alpha1().ElasticQuotaProfiles("").Create(context.TODO(), profile, metav1.CreateOptions{})
	assert.Nil(t, err)

	time.Sleep(100 * time.Millisecond)
	mgr := plugin.GetGroupQuotaManager(profile.Name)
	assert.NotNil(t, mgr)
	assert.Equal(t, mgr.GetClusterTotalResource(), createResourceList(200, 2000))
	assert.Equal(t, plugin.groupQuotaManager.GetClusterTotalResource(), createResourceList(300, 3000))

	// update profile  tests
	updatedProfile.Spec.NodeSelector.MatchLabels["topology.kubernetes.io/zone"] = "cn-hangzhou-b"
	updatedProfile.ResourceVersion = "5"
	_, err = plugin.koordClient.QuotaV1alpha1().ElasticQuotaProfiles("").Update(context.TODO(), updatedProfile, metav1.UpdateOptions{})
	assert.Nil(t, err)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, mgr.GetClusterTotalResource(), createResourceList(100, 1000))
	assert.Equal(t, plugin.groupQuotaManager.GetClusterTotalResource(), createResourceList(300, 3000))

	// delete profile tests
	err = plugin.koordClient.QuotaV1alpha1().ElasticQuotaProfiles("").Delete(context.TODO(), profile.Name, metav1.DeleteOptions{})
	assert.Nil(t, err)

	time.Sleep(100 * time.Millisecond)
	mgr = plugin.GetGroupQuotaManager(profile.Name)
	assert.Nil(t, mgr)
	assert.Equal(t, plugin.groupQuotaManager.GetClusterTotalResource(), createResourceList(300, 3000))
}
