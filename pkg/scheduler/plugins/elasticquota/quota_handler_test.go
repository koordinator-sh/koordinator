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
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func TestPlugin_OnQuotaAddWithTreeID(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.MultiQuotaTree, true)()

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	plugin.groupQuotaManager.UpdateClusterTotalResource(createResourceList(501952056, 0))
	gqm := plugin.groupQuotaManager
	quota1 := suit.AddQuota("1", "", 0, 0, 0, 0, 0, 0, false, "")
	assert.NotNil(t, gqm.GetQuotaInfoByName("1"))
	quota1.DeletionTimestamp = &metav1.Time{Time: time.Now()}

	quota2 := quota1.DeepCopy()
	quota2.Name = "2"
	plugin.OnQuotaAdd(quota2)
	assert.Nil(t, gqm.GetQuotaInfoByName("2"))

	suit.AddQuotaWithTreeID("3", "", 0, 0, 0, 0, 0, 0, false, "", "tree-a")
	suit.AddQuotaWithTreeID("4", "", 0, 0, 0, 0, 0, 0, false, "", "tree-b")

	gqmA := plugin.GetGroupQuotaManagerForTree("tree-a")
	assert.NotNil(t, gqmA)
	assert.NotNil(t, gqmA.GetQuotaInfoByName("3"))
	assert.Nil(t, gqmA.GetQuotaInfoByName("4"))

	gqmB := plugin.GetGroupQuotaManagerForTree("tree-b")
	assert.NotNil(t, gqmB)
	assert.Nil(t, gqmB.GetQuotaInfoByName("3"))
	assert.NotNil(t, gqmB.GetQuotaInfoByName("4"))

	assert.Nil(t, gqm.GetQuotaInfoByName("3"))
	assert.Nil(t, gqm.GetQuotaInfoByName("4"))

}

func TestPlugin_OnQuotaUpdateAndDeleteWithTreeID(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.MultiQuotaTree, true)()

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	// tree-b:
	// test2 Max[100, 200]  Min[100,160] request[20,40]
	//   `-- test2-a Max[100, 200]  Min[50,80] request[20,40]
	// tree-a:
	// test1 Max[100, 200]  Min[100,160] request[80,200]
	//   `-- test1-a Max[100, 200]  Min[50,80] request[40,100]
	//         `-- a-123 Max[100, 200]  Min[50,80] request[40,100]
	//   `-- test1-b Max[100, 200]  Min[50,80] request[40,100]
	//         `-- b-123 Max[100, 200]  Min[50,80] request[40,100]
	plugin.addQuota("test1", extension.RootQuotaName, 100, 200, 100, 160, 96, 160, true, "", "tree-a")
	plugin.addQuota("test1-a", "test1", 100, 200, 50, 80, 96, 160, true, "", "tree-a")
	plugin.addQuota("a-123", "test1-a", 100, 200, 50, 80, 96, 160, false, "", "tree-a")
	plugin.addQuota("test1-b", "test1", 100, 200, 50, 80, 96, 160, true, "", "tree-a")
	changeQuota := plugin.addQuota("b-123", "test1-b", 100, 200, 50, 80, 96, 160, false, "", "tree-a")

	plugin.addQuota("test2", extension.RootQuotaName, 100, 200, 100, 160, 96, 160, true, "", "tree-b")
	plugin.addQuota("test2-a", "test2", 100, 200, 50, 80, 96, 160, false, "", "tree-b")

	gqm := plugin.GetGroupQuotaManagerForTree("")
	assert.NotNil(t, gqm)
	gqmA := plugin.GetGroupQuotaManagerForTree("tree-a")
	assert.NotNil(t, gqmA)
	gqmB := plugin.GetGroupQuotaManagerForTree("tree-b")
	assert.NotNil(t, gqmA)

	gqm.UpdateClusterTotalResource(createResourceList(200, 400))
	gqmA.UpdateClusterTotalResource(createResourceList(100, 200))
	gqmB.UpdateClusterTotalResource(createResourceList(100, 200))

	// a-123 request [40,100]
	request1 := createResourceList(40, 100)
	pod1 := makePod2("pod", request1)
	pod1.Labels[extension.LabelQuotaName] = "a-123"
	plugin.OnPodAdd(pod1)
	runtime := gqmA.RefreshRuntime("a-123")
	assert.Equal(t, request1, runtime)

	runtime = gqmA.RefreshRuntime("test1-a")
	assert.Equal(t, request1, runtime)

	runtime = gqmA.RefreshRuntime("test1")
	assert.Equal(t, request1, runtime)

	// b-123 request [40,100]
	request2 := createResourceList(40, 100)
	pod2 := makePod2("pod", request2)
	pod2.Labels[extension.LabelQuotaName] = "b-123"
	plugin.OnPodAdd(pod2)
	runtime = gqmA.RefreshRuntime("b-123")
	assert.Equal(t, request2, runtime)

	runtime = gqmA.RefreshRuntime("test1-b")
	assert.Equal(t, request2, runtime)

	sumRequest := createResourceList(80, 200)
	runtime = gqmA.RefreshRuntime("test1")
	assert.Equal(t, sumRequest, runtime)

	// test2-a request [20,40]
	request3 := createResourceList(20, 40)
	pod3 := makePod2("pod1", request3)
	pod3.Labels[extension.LabelQuotaName] = "test2-a"
	plugin.OnPodAdd(pod3)
	runtime = gqmB.RefreshRuntime("test2-a")
	assert.Equal(t, request3, runtime)

	runtime = gqmB.RefreshRuntime("test2")
	assert.Equal(t, request3, runtime)

	// mv b-123 form test1-b to test1-a
	// tree-b:
	// test2 Max[100, 200]  Min[100,160] request[20,40]
	//   `-- test2-a Max[100, 200]  Min[50,80] request[20,40]
	// tree-a:
	// test1 Max[100, 200]  Min[100,160] request[80,200]
	//   `-- test1-a Max[100, 200]  Min[50,80] request[80,200]
	//         `-- a-123 Max[100, 200]  Min[50,80] request[40,100]
	//         `-- b-123 Max[100, 200]  Min[50,80] request[40,100]
	//   `-- test1-b Max[100, 200]  Min[50,80] request[0,0]
	oldQuota := changeQuota.DeepCopy()
	changeQuota.Labels[extension.LabelQuotaParent] = "test1-a"
	changeQuota.ResourceVersion = "2"
	//gqm.GetQuotaInfoByName("test1-a").IsParent = true

	plugin.OnQuotaUpdate(oldQuota, changeQuota)
	quotaInfo := gqmA.GetQuotaInfoByName("test1-b")
	gqmA.RefreshRuntime("test1-b")
	assert.Equal(t, corev1.ResourceList{}, quotaInfo.GetRequest())
	assert.Equal(t, corev1.ResourceList{}, quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(0, 0), quotaInfo.GetRuntime())

	quotaInfo = gqmA.GetQuotaInfoByName("test1-a")
	gqmA.RefreshRuntime("test1-a")
	assert.Equal(t, createResourceList(80, 200), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(80, 200), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(80, 200), quotaInfo.GetRuntime())

	quotaInfo = gqmA.GetQuotaInfoByName("a-123")
	gqmA.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRuntime())

	quotaInfo = gqmA.GetQuotaInfoByName("b-123")
	gqmA.RefreshRuntime("b-123")
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRuntime())
	assert.Equal(t, "test1-a", quotaInfo.ParentName)

	quotaInfo = gqmA.GetQuotaInfoByName("test1")
	gqmA.RefreshRuntime("test1")
	assert.Equal(t, createResourceList(80, 200), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(80, 200), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(80, 200), quotaInfo.GetRuntime())

	// delete b-123

	// tree-b:
	// test2 Max[100, 200]  Min[100,160] request[20,40]
	//   `-- test2-a Max[100, 200]  Min[50,80] request[20,40]
	// tree-a:
	// test1 Max[100, 200]  Min[100,160] request[40,100]
	//   `-- test1-a Max[100, 200]  Min[50,80] request[40,100]
	//         `-- a-123 Max[100, 200]  Min[50,80] request[40,100]
	//   `-- test1-b Max[100, 200]  Min[50,80] request[0,0]
	plugin.OnQuotaDelete(changeQuota)
	gqmA.RefreshRuntime("test1-a")
	quotaInfo = gqmA.GetQuotaInfoByName("test1-a")
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRuntime())

	gqmA.RefreshRuntime("test1-b")
	quotaInfo = gqmA.GetQuotaInfoByName("test1-b")
	assert.Equal(t, corev1.ResourceList{}, quotaInfo.GetRequest())
	assert.Equal(t, corev1.ResourceList{}, quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(0, 0), quotaInfo.GetRuntime())

	quotaInfo = gqmA.GetQuotaInfoByName("test1")
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRuntime())

	gqmB.RefreshRuntime("test2-a")
	quotaInfo = gqmB.GetQuotaInfoByName("test2-a")
	assert.Equal(t, createResourceList(20, 40), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(20, 40), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(20, 40), quotaInfo.GetRuntime())

	quotaInfo = gqmB.GetQuotaInfoByName("test2")
	assert.Equal(t, createResourceList(20, 40), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(20, 40), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(20, 40), quotaInfo.GetRuntime())
}

func TestPlugin_OnRootQuotaAddAndUpdate(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.MultiQuotaTree, true)()

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	// tree-cn-hangzhou-a:
	// cn-hangzhou-a Max[100, 200]  Min[100,200] request[90,190]
	//   `-- test1-a Max[100, 200]  Min[40,80] request[40,100]
	//   `-- test1-b Max[100, 200]  Min[40,80] request[50,90]
	rootQuota := plugin.addRootQuota("cn-hangzhou-a", "", 100, 200, 100, 200, 100, 200, true, "", "tree-cn-hangzhou-a")
	gqmA := plugin.GetGroupQuotaManagerForTree("tree-cn-hangzhou-a")
	assert.NotNil(t, gqmA)
	assert.True(t, quotav1.Equals(createResourceList(100, 200), gqmA.GetClusterTotalResource()))

	plugin.addQuota("test1-a", "cn-hangzhou-a", 100, 200, 40, 80, 100, 200, false, "", "tree-cn-hangzhou-a")
	plugin.addQuota("test1-b", "cn-hangzhou-a", 100, 200, 40, 80, 100, 200, false, "", "tree-cn-hangzhou-a")

	// add pod1
	request1 := createResourceList(40, 100)
	pod1 := makePod2("pod1", request1)
	pod1.Labels[extension.LabelQuotaName] = "test1-a"
	plugin.OnPodAdd(pod1)
	gqmA.RefreshRuntime("test1-a")

	quotaInfo := gqmA.GetQuotaInfoByName("test1-a")
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRuntime())

	quotaInfo = gqmA.GetQuotaInfoByName("cn-hangzhou-a")
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(40, 100), quotaInfo.GetRuntime())

	// add pod2
	request2 := createResourceList(50, 90)
	pod2 := makePod2("pod2", request2)
	pod2.Labels[extension.LabelQuotaName] = "test1-b"
	plugin.OnPodAdd(pod2)
	gqmA.RefreshRuntime("test1-b")

	quotaInfo = gqmA.GetQuotaInfoByName("test1-b")
	assert.Equal(t, createResourceList(50, 90), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(50, 90), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(50, 90), quotaInfo.GetRuntime())

	quotaInfo = gqmA.GetQuotaInfoByName("cn-hangzhou-a")
	assert.Equal(t, createResourceList(90, 190), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(90, 190), quotaInfo.GetUsed())
	assert.Equal(t, createResourceList(90, 190), quotaInfo.GetRuntime())

	// update root quota
	copy := rootQuota.DeepCopy()
	copy.ResourceVersion = "12"
	copy.Annotations[extension.AnnotationTotalResource] = fmt.Sprintf("{\"cpu\":%v, \"memory\":\"%v\"}", 200, 400)
	plugin.OnQuotaUpdate(rootQuota, copy)

	assert.True(t, quotav1.Equals(createResourceList(200, 400), gqmA.GetClusterTotalResource()))
}

func TestPlugin_HandlerQuotaWhenRoot(t *testing.T) {
	nodes := []*corev1.Node{
		defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
		defaultCreateNodeWithLabels("node2", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
		defaultCreateNodeWithLabels("node3", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-b"}),
	}

	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.MultiQuotaTree, true)()
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	for _, node := range nodes {
		plugin.OnNodeAdd(node)
	}

	assert.True(t, quotav1.Equals(createResourceList(300, 3000), plugin.groupQuotaManager.GetClusterTotalResource()))

	rootQuota := plugin.addRootQuota("cn-hangzhou-a", "", 200, 2000, 200, 2000, 200, 2000, true, "", "tree-cn-hangzhou-a")
	gqmA := plugin.GetGroupQuotaManagerForTree("tree-cn-hangzhou-a")

	assert.True(t, quotav1.Equals(createResourceList(200, 2000), gqmA.GetClusterTotalResource()))
	assert.True(t, quotav1.Equals(createResourceList(100, 1000), plugin.groupQuotaManager.GetClusterTotalResource()))

	copy := rootQuota.DeepCopy()
	copy.ResourceVersion = "12"
	copy.Annotations[extension.AnnotationTotalResource] = fmt.Sprintf("{\"cpu\":%v, \"memory\":\"%v\"}", 250, 2500)

	plugin.OnQuotaUpdate(rootQuota, copy)

	assert.True(t, quotav1.Equals(createResourceList(250, 2500), gqmA.GetClusterTotalResource()))
	assert.True(t, quotav1.Equals(createResourceList(50, 500), plugin.groupQuotaManager.GetClusterTotalResource()))

	plugin.OnQuotaDelete(copy)
	assert.True(t, quotav1.Equals(createResourceList(300, 3000), plugin.groupQuotaManager.GetClusterTotalResource()))
}

func TestPlugin_ReplaceQuotas(t *testing.T) {
	quotas := []interface{}{
		CreateQuota2("test1", extension.RootQuotaName, 100, 200, 40, 80, 1, 1, true, ""),
		CreateQuota2("test2", extension.RootQuotaName, 200, 400, 80, 160, 1, 1, true, ""),
		CreateQuota2("test11", "test1", 100, 200, 40, 80, 1, 1, false, ""),
	}

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)

	// ReplaceQuotas will conflict with QuotaEventHandler. sleep 1 seconds to avoid it.
	time.Sleep(time.Second)
	plugin.ReplaceQuotas(quotas)

	plugin.groupQuotaManager.UpdateClusterTotalResource(createResourceList(1000, 1000))

	// test11 request [40,100]
	request1 := createResourceList(40, 100)
	pod1 := makePod2("pod", request1)
	pod1.Labels[extension.LabelQuotaName] = "test11"
	plugin.OnPodAdd(pod1)
	runtime := plugin.groupQuotaManager.RefreshRuntime("test11")
	assert.Equal(t, request1, runtime)

	runtime = plugin.groupQuotaManager.RefreshRuntime("test1")
	assert.Equal(t, request1, runtime)

	runtime = plugin.groupQuotaManager.RefreshRuntime("test2")
	assert.Equal(t, createResourceList(0, 0), runtime)
}
