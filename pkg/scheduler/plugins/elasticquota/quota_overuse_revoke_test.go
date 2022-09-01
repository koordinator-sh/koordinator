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
	"k8s.io/apimachinery/pkg/types"
	v1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func TestLessEqual(t *testing.T) {
	{
		lhs := createResourceList(0, 0)
		lhs["ali"] = *resource.NewQuantity(10, resource.DecimalSI)
		rhs := createResourceList(0, 0)
		ok, _ := v1.LessThanOrEqual(lhs, rhs)
		if !ok {
			t.Error("error")
		}
		rhs["ali"] = *resource.NewQuantity(11, resource.DecimalSI)
		ok, _ = v1.LessThanOrEqual(lhs, rhs)
		if !ok {
			t.Error("error")
		}
		rhs["ali"] = *resource.NewQuantity(9, resource.DecimalSI)
		ok, _ = v1.LessThanOrEqual(lhs, rhs)
		if ok {
			t.Error("error")
		}
	}
}

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
	pg.quotaOverUsedRevokeController.SyncQuota()
	monitor := pg.quotaOverUsedRevokeController.group2MonitorMap["odpsbatch"]
	{
		usedQuota := createResourceList(0, 0)
		usedQuota["ali"] = *resource.NewQuantity(10000, resource.DecimalSI)
		gqm.UpdateGroupDeltaUsed("odpsbatch", usedQuota)

		result := monitor.Monitor()
		if result || monitor.GetOverUsedContinueCount() != 0 {
			t.Error("error")
		}
	}
	{
		usedQuota := createResourceList(1000, 0)
		usedQuota["ali"] = *resource.NewQuantity(10000, resource.DecimalSI)

		gqm.UpdateGroupDeltaUsed("odpsbatch", usedQuota)
		result := monitor.Monitor()
		if result || monitor.GetOverUsedContinueCount() != 1 {
			t.Errorf("error")
		}
	}
	{
		usedQuota := createResourceList(-1000, 0)
		usedQuota["ali"] = *resource.NewQuantity(10000, resource.DecimalSI)

		gqm.UpdateGroupDeltaUsed("odpsbatch", usedQuota)
		result := monitor.Monitor()
		if result || monitor.GetOverUsedContinueCount() != 0 {
			t.Errorf("error")
		}
	}
	{
		usedQuota := createResourceList(1000, 0)
		usedQuota["ali"] = *resource.NewQuantity(10000, resource.DecimalSI)
		gqm.UpdateGroupDeltaUsed("odpsbatch", usedQuota)

		for i := int64(1); i <= monitor.continueOverUsedCountTriggerEvict-1; i++ {
			result := monitor.Monitor()
			if result || monitor.GetOverUsedContinueCount() != i {
				t.Errorf("error,expect:%v, actual:%v", i, monitor.GetOverUsedContinueCount())
			}
		}

		result := monitor.Monitor()
		if !result || monitor.GetOverUsedContinueCount() != 0 {
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
	con := plugin.quotaOverUsedRevokeController
	con.SyncQuota()
	con.addPodWithQuotaName("odpsbatch", defaultCreatePod("1", 10, 30, 0))
	con.addPodWithQuotaName("odpsbatch", defaultCreatePod("2", 9, 10, 1))
	con.addPodWithQuotaName("odpsbatch", defaultCreatePod("3", 8, 20, 0))
	con.addPodWithQuotaName("odpsbatch", defaultCreatePod("4", 7, 40, 0))

	result := con.group2MonitorMap["odpsbatch"].GetToRevokePodList("odpsbatch")
	if len(result) != 2 {
		t.Errorf("error:%v", len(result))
	}
	if result[0].Name != "2" || result[1].Name != "4" {
		t.Errorf("error")
	}
	gqm.GetQuotaInfoByName("odpsbatch").CalculateInfo.Runtime = createResourceList(-1, 0)
	result = con.group2MonitorMap["odpsbatch"].GetToRevokePodList("odpsbatch")
	if len(result) != 4 {
		t.Errorf("error:%v", len(result))
	}
	con.erasePodWithQuotaName("odpsbatch", defaultCreatePod("4", 7, 40, 0))
	result = con.group2MonitorMap["odpsbatch"].GetToRevokePodList("odpsbatch")
	if len(result) != 3 {
		t.Errorf("error:%v", len(result))
	}
}

func defaultCreatePodWithQuotaNameAndVersion(name, quotaName, version string, priority int32, cpu, mem int64) *corev1.Pod {
	pod := defaultCreatePod(name, priority, cpu, mem)
	pod.Labels[extension.LabelQuotaName] = quotaName
	pod.ResourceVersion = version
	pod.UID = types.UID(name)
	return pod
}

func defaultCreatePodWithQuotaName(name, quotaName string, priority int32, cpu, mem int64) *corev1.Pod {
	pod := defaultCreatePod(name, priority, cpu, mem)
	pod.Labels[extension.LabelQuotaName] = quotaName
	pod.UID = types.UID(name)
	return pod
}

func defaultCreatePod(name string, priority int32, cpu, mem int64) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: make(map[string]string),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: createResourceList(cpu, mem),
					},
				},
			},
			Priority: pointer.Int32(priority),
		},
	}
	return pod
}

func TestQuotaOverUsedRevokeController_GetToMonitorQuotas(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, _ := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	plugin := p.(*Plugin)
	gqm := plugin.groupQuotaManager
	gqm.UpdateClusterTotalResource(createResourceList(10850060000, 0))
	cc := plugin.quotaOverUsedRevokeController
	cc.continueOverUsedCountTriggerEvict = 0
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

	cc.SyncQuota()
	result := cc.GetToMonitorQuotas()
	if len(result) != 4 || result["aliyun1"] == nil || result["aliyun2"] == nil || result["aliyun3"] == nil {
		t.Errorf("error")
	}
	assert.Equal(t, cc.GetGroup2MonitorMapLen(), 4)
	suit.client.SchedulingV1alpha1().ElasticQuotas("ali").Delete(context.TODO(), "aliyun1", metav1.DeleteOptions{})
	time.Sleep(200 * time.Millisecond)
	cc.SyncQuota()
	assert.Equal(t, cc.GetGroup2MonitorMapLen(), 3)
	cc.group2MonitorMapLock.RLock()
	if cc.group2MonitorMap["aliyun1"] != nil {
		t.Errorf("error")
	}
	cc.group2MonitorMapLock.RUnlock()
}

func (controller *QuotaOverUsedRevokeController) GetGroup2MonitorMapLen() int {
	controller.group2MonitorMapLock.RLock()
	defer controller.group2MonitorMapLock.RUnlock()

	return len(controller.group2MonitorMap)
}

func (monitor *QuotaOverUsedGroupMonitor) GetOverUsedContinueCount() int64 {
	return monitor.overUsedContinueCount
}
