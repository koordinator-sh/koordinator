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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/tools/cache"

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func TestPlugin_OnPodAddAndDeleteWhenDisableDefault(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.DisableDefaultQuota, true)()

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	suit.AddQuota("test", "", 10, 40, 10, 40, 10, 40, false, "")

	defaultQuotaInfo := plugin.groupQuotaManager.GetQuotaInfoByName(extension.DefaultQuotaName)
	testQuotaInfo := plugin.groupQuotaManager.GetQuotaInfoByName("test")

	// no quota label
	pod1 := makePod2("pod1", MakeResourceList().CPU(1).Mem(2).Obj())
	plugin.OnPodAdd(pod1)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(testQuotaInfo.GetUsed(), v1.ResourceList{}))

	// none-exist quota label
	pod2 := makePod2("pod2", MakeResourceList().CPU(1).Mem(2).Obj())
	pod2.Labels[extension.LabelQuotaName] = "none-exist"
	plugin.OnPodAdd(pod2)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(testQuotaInfo.GetUsed(), v1.ResourceList{}))

	// normal
	pod3 := makePod2("pd3", MakeResourceList().CPU(1).Mem(2).Obj())
	pod3.Labels[extension.LabelQuotaName] = "test"
	plugin.OnPodAdd(pod3)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(testQuotaInfo.GetUsed(), MakeResourceList().CPU(1).Mem(2).Obj()))

	// delete pod
	plugin.OnPodDelete(pod3)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(testQuotaInfo.GetUsed(), MakeResourceList().CPU(0).Mem(0).Obj()))
}

func TestPlugin_OnPodUpdateWhenDisableDefault(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.DisableDefaultQuota, true)()

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	suit.AddQuota("test1", "", 10, 40, 10, 40, 10, 40, false, "")
	suit.AddQuota("test2", "", 10, 40, 10, 40, 10, 40, false, "")

	defaultQuotaInfo := plugin.groupQuotaManager.GetQuotaInfoByName(extension.DefaultQuotaName)
	test1QuotaInfo := plugin.groupQuotaManager.GetQuotaInfoByName("test1")
	test2QuotaInfo := plugin.groupQuotaManager.GetQuotaInfoByName("test2")

	// pod1 belong to quota test1
	pod1 := makePod2("pod1", MakeResourceList().CPU(1).Mem(2).Obj())
	pod1.Labels[extension.LabelQuotaName] = "test1"
	plugin.OnPodAdd(pod1)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(test1QuotaInfo.GetUsed(), MakeResourceList().CPU(1).Mem(2).Obj()))
	assert.True(t, quotav1.Equals(test2QuotaInfo.GetUsed(), v1.ResourceList{}))

	// change quota from test1 to test2
	pod2 := pod1.DeepCopy()
	pod2.ResourceVersion = "3"
	pod2.Labels[extension.LabelQuotaName] = "test2"
	plugin.OnPodUpdate(pod1, pod2)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(test1QuotaInfo.GetUsed(), MakeResourceList().CPU(0).Mem(0).Obj()))
	assert.True(t, quotav1.Equals(test2QuotaInfo.GetUsed(), MakeResourceList().CPU(1).Mem(2).Obj()))

	// remove quota label
	pod3 := pod1.DeepCopy()
	pod3.ResourceVersion = "4"
	delete(pod3.Labels, extension.LabelQuotaName)
	plugin.OnPodUpdate(pod2, pod3)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(test1QuotaInfo.GetUsed(), MakeResourceList().CPU(0).Mem(0).Obj()))
	assert.True(t, quotav1.Equals(test2QuotaInfo.GetUsed(), MakeResourceList().CPU(0).Mem(0).Obj()))
}

func TestPlugin_OnPodUpdateWhenDisableDefaultAndRootTree(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.DisableDefaultQuota, true)()
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.MultiQuotaTree, true)()

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	plugin.addRootQuota("test1", "", 10, 40, 10, 40, 10, 40, false, "", "tree1")
	plugin.addRootQuota("test2", "", 10, 40, 10, 40, 10, 40, false, "", "tree2")

	gqm := plugin.GetGroupQuotaManagerForTree("")
	gqm1 := plugin.GetGroupQuotaManagerForTree("tree1")
	gqm2 := plugin.GetGroupQuotaManagerForTree("tree2")

	defaultQuotaInfo := gqm.GetQuotaInfoByName(extension.DefaultQuotaName)

	test1QuotaInfo := gqm1.GetQuotaInfoByName("test1")
	assert.NotNil(t, test1QuotaInfo)

	test2QuotaInfo := gqm2.GetQuotaInfoByName("test2")
	assert.NotNil(t, test2QuotaInfo)

	// no quota label
	pod1 := makePod2("pod1", MakeResourceList().CPU(1).Mem(2).Obj())
	plugin.OnPodAdd(pod1)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(test1QuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(test2QuotaInfo.GetUsed(), v1.ResourceList{}))

	// add to quota test1
	pod2 := pod1.DeepCopy()
	pod2.ResourceVersion = "2"
	pod2.Labels[extension.LabelQuotaName] = "test1"
	plugin.OnPodUpdate(pod1, pod2)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(test1QuotaInfo.GetUsed(), MakeResourceList().CPU(1).Mem(2).Obj()))
	assert.True(t, quotav1.Equals(test2QuotaInfo.GetUsed(), v1.ResourceList{}))

	fmt.Println("xxx", test1QuotaInfo.GetUsed())
	fmt.Println("yyy", test2QuotaInfo.GetUsed())

	// change from quota test1 to test2
	pod3 := pod2.DeepCopy()
	pod3.ResourceVersion = "3"
	pod3.Labels[extension.LabelQuotaName] = "test2"
	plugin.OnPodUpdate(pod2, pod3)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(test1QuotaInfo.GetUsed(), MakeResourceList().CPU(0).Mem(0).Obj()))
	assert.True(t, quotav1.Equals(test2QuotaInfo.GetUsed(), MakeResourceList().CPU(1).Mem(2).Obj()))

	// remove quota label
	pod4 := pod3.DeepCopy()
	pod4.ResourceVersion = "4"
	delete(pod4.Labels, extension.LabelQuotaName)
	plugin.OnPodUpdate(pod3, pod4)
	assert.True(t, quotav1.Equals(defaultQuotaInfo.GetUsed(), v1.ResourceList{}))
	assert.True(t, quotav1.Equals(test1QuotaInfo.GetUsed(), MakeResourceList().CPU(0).Mem(0).Obj()))
	assert.True(t, quotav1.Equals(test2QuotaInfo.GetUsed(), MakeResourceList().CPU(0).Mem(0).Obj()))
}

func TestPlugin_OnPodDelete(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.DisableDefaultQuota, true)()

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	suit.AddQuota("test", "", 10, 40, 10, 40, 10, 40, false, "")

	testQuotaInfo := plugin.groupQuotaManager.GetQuotaInfoByName("test")

	assert.True(t, quotav1.Equals(testQuotaInfo.GetUsed(), v1.ResourceList{}))

	pod := makePod2("pod", MakeResourceList().CPU(1).Mem(2).Obj())
	pod.Labels[extension.LabelQuotaName] = "test"

	tests := []struct {
		name         string
		obj          interface{}
		wantResource v1.ResourceList
	}{
		{
			name:         "object is pod",
			obj:          pod,
			wantResource: MakeResourceList().CPU(0).Mem(0).Obj(),
		},
		{
			name: "object is DeletedFinalStateUnknown{Obj: pod}",
			obj: cache.DeletedFinalStateUnknown{
				Key: "test",
				Obj: pod,
			},
			wantResource: MakeResourceList().CPU(0).Mem(0).Obj(),
		},
		{
			name:         "object is node",
			obj:          &v1.Node{},
			wantResource: MakeResourceList().CPU(1).Mem(2).Obj(),
		},
		{
			name: "object is DeletedFinalStateUnknown{Obj: node}",
			obj: cache.DeletedFinalStateUnknown{
				Key: "test",
				Obj: &v1.Node{},
			},
			wantResource: MakeResourceList().CPU(1).Mem(2).Obj(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin.OnPodAdd(pod)
			plugin.OnPodDelete(tt.obj)
			assert.True(t, quotav1.Equals(testQuotaInfo.GetUsed(), tt.wantResource))
		})
	}
}
