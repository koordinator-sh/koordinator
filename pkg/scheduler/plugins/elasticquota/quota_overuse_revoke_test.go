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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestQuotaOverUsedGroupMonitor_Monitor(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, _ := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	pg := p.(*Plugin)
	pg.groupQuotaManager.UpdateClusterTotalResource(MakeResourceList().CPU(100).Mem(100).GPU(100).Obj())
	suit.AddQuota("odpsbatch", "root", 4797411900, 0, 1085006000, 0, 4797411900, 0, true, "ali")
	gqm := pg.groupQuotaManager
	gqm.UpdateClusterTotalResource(createResourceList(100, 1000))
	gqm.UpdateGroupDeltaRequest("odpsbatch", createResourceList(1000, 100000))
	gqm.RefreshRuntime("odpsbatch")
	quotaOverUsedRevokeController := NewQuotaOverUsedRevokeController(pg.handle.ClientSet(), pg.pluginArgs.DelayEvictTime.Duration,
		pg.pluginArgs.RevokePodInterval.Duration, pg.groupQuotaManager, *pg.pluginArgs.MonitorAllQuotas)
	quotaOverUsedRevokeController.syncQuota()
	monitor := quotaOverUsedRevokeController.monitors["odpsbatch"]
	{
		usedQuota := createResourceList(0, 0)
		usedQuota["ali"] = *resource.NewQuantity(10000, resource.DecimalSI)
		gqm.UpdateGroupDeltaUsed("odpsbatch", usedQuota)

		result := monitor.monitor()
		if result {
			t.Error("error")
		}
	}
	{
		usedQuota := createResourceList(1000, 0)
		usedQuota["ali"] = *resource.NewQuantity(10000, resource.DecimalSI)

		gqm.UpdateGroupDeltaUsed("odpsbatch", usedQuota)
		result := monitor.monitor()
		if result {
			t.Errorf("error")
		}
	}
	{
		usedQuota := createResourceList(-1000, 0)
		usedQuota["ali"] = *resource.NewQuantity(10000, resource.DecimalSI)

		gqm.UpdateGroupDeltaUsed("odpsbatch", usedQuota)
		result := monitor.monitor()
		if result {
			t.Errorf("error")
		}
	}
	{
		usedQuota := createResourceList(1000, 0)
		usedQuota["ali"] = *resource.NewQuantity(10000, resource.DecimalSI)
		gqm.UpdateGroupDeltaUsed("odpsbatch", usedQuota)
		monitor.overUsedTriggerEvictDuration = 0 * time.Second

		result := monitor.monitor()
		if !result {
			t.Errorf("error")
		}
	}
}

func TestQuotaOverUsedRevokeController_GetToRevokePodList(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, _ := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	plugin := p.(*Plugin)
	gqm := plugin.groupQuotaManager
	suit.AddQuota("odpsbatch", "root", 4797411900, 0, 1085006000, 0, 4797411900, 0, true, "ali")
	time.Sleep(10 * time.Millisecond)
	gqm.UpdateGroupDeltaUsed("odpsbatch", createResourceList(100, 1))
	gqm.GetQuotaInfoByName("odpsbatch").CalculateInfo.Runtime = createResourceList(50, 0)
	con := NewQuotaOverUsedRevokeController(plugin.handle.ClientSet(), plugin.pluginArgs.DelayEvictTime.Duration,
		plugin.pluginArgs.RevokePodInterval.Duration, plugin.groupQuotaManager, *plugin.pluginArgs.MonitorAllQuotas)
	con.syncQuota()
	quotaInfo := gqm.GetQuotaInfoByName("odpsbatch")
	quotaInfo.AddPodIfNotPresent(defaultCreatePod("1", 10, 30, 0))
	quotaInfo.AddPodIfNotPresent(defaultCreatePod("2", 9, 10, 1))
	quotaInfo.AddPodIfNotPresent(defaultCreatePod("3", 8, 20, 0))
	pod4 := defaultCreatePod("4", 7, 40, 0)
	quotaInfo.AddPodIfNotPresent(pod4)
	quotaInfo.UpdatePodIsAssigned("1", true)
	quotaInfo.UpdatePodIsAssigned("3", true)
	quotaInfo.UpdatePodIsAssigned("2", true)
	quotaInfo.UpdatePodIsAssigned("4", true)

	result := con.monitors["odpsbatch"].getToRevokePodList("odpsbatch")
	if len(result) != 2 {
		t.Errorf("error:%v", len(result))
	}
	if result[0].Name != "2" || result[1].Name != "4" {
		t.Errorf("error")
	}
	gqm.GetQuotaInfoByName("odpsbatch").CalculateInfo.Runtime = createResourceList(-1, 0)
	result = con.monitors["odpsbatch"].getToRevokePodList("odpsbatch")
	if len(result) != 4 {
		t.Errorf("error:%v", len(result))
	}
	err := quotaInfo.UpdatePodIsAssigned("4", false)
	pod4.Status.Phase = corev1.PodPending
	assert.Nil(t, err)
	result = con.monitors["odpsbatch"].getToRevokePodList("odpsbatch")
	if len(result) != 3 {
		t.Errorf("error:%v", len(result))
	}
}

func TestQuotaOverUsedRevokeController_GetToMonitorQuotas(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, _ := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	plugin := p.(*Plugin)
	gqm := plugin.groupQuotaManager
	gqm.UpdateClusterTotalResource(createResourceList(10850060000, 0))
	cc := NewQuotaOverUsedRevokeController(plugin.handle.ClientSet(), 0*time.Second,
		plugin.pluginArgs.RevokePodInterval.Duration, plugin.groupQuotaManager, *plugin.pluginArgs.MonitorAllQuotas)

	suit.AddQuota("aliyun1", "root", 4797411900, 0, 1085006000, 0, 4797411900, 0, true, "ali")
	suit.AddQuota("aliyun2", "root", 4797411900, 0, 1085006000, 0, 4797411900, 0, true, "ali")
	suit.AddQuota("aliyun3", "root", 4797411900, 0, 1085006000, 0, 4797411900, 0, true, "ali")
	time.Sleep(10 * time.Millisecond)
	gqm.UpdateGroupDeltaUsed("aliyun1", createResourceList(100, 0))
	gqm.UpdateGroupDeltaUsed("aliyun2", createResourceList(100, 0))
	gqm.UpdateGroupDeltaUsed("aliyun3", createResourceList(100, 0))
	gqm.GetQuotaInfoByName("aliyun1").CalculateInfo.Runtime = createResourceList(10, 0)
	gqm.GetQuotaInfoByName("aliyun2").CalculateInfo.Runtime = createResourceList(10, 0)
	gqm.GetQuotaInfoByName("aliyun3").CalculateInfo.Runtime = createResourceList(10, 0)

	cc.syncQuota()
	result := cc.getToMonitorQuotas()
	if len(result) != 4 || result["aliyun1"] == nil || result["aliyun2"] == nil || result["aliyun3"] == nil {
		t.Errorf("error")
	}
	assert.Equal(t, cc.GetmonitorsLen(), 4)
	suit.client.SchedulingV1alpha1().ElasticQuotas("ali").Delete(context.TODO(), "aliyun1", metav1.DeleteOptions{})
	time.Sleep(200 * time.Millisecond)
	cc.syncQuota()
	assert.Equal(t, cc.GetmonitorsLen(), 3)
	cc.monitorsLock.RLock()
	if cc.monitors["aliyun1"] != nil {
		t.Errorf("error")
	}
	cc.monitorsLock.RUnlock()
}

func (controller *QuotaOverUsedRevokeController) GetmonitorsLen() int {
	controller.monitorsLock.RLock()
	defer controller.monitorsLock.RUnlock()

	return len(controller.monitors)
}

func (monitor *QuotaOverUsedGroupMonitor) GetLastUnderUseTime() time.Time {
	return monitor.lastUnderUsedTime
}
