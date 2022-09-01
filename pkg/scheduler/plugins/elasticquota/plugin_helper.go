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
	"fmt"

	v1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedulerv1alpha1 "sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

// getPodAssociateQuotaName If pod's don't have the "quota-name" label, we will use the namespace to associate pod with quota
// group. If the plugin can't find the matched quota group, it will force the pod to associate with the "default-group".
func (g *Plugin) getPodAssociateQuotaName(pod *v1.Pod) string {
	quotaName := pod.Labels[extension.LabelQuotaName]
	if quotaName == "" {
		list, err := g.elasticQuotaController.eqLister.ElasticQuotas(pod.Namespace).List(labels.Everything())
		if err != nil {
			runtime.HandleError(err)
			return extension.DefaultQuotaName
		}
		if len(list) == 0 {
			return extension.DefaultQuotaName
		}
		// todo when elastic quota supports multiple instances in a namespace, modify this
		quotaName = list[0].Name
	}
	// can't get the quotaInfo by quotaName, let the pod belongs to DefaultQuotaGroup
	if g.groupQuotaManager.GetQuotaInfoByName(quotaName) == nil {
		quotaName = extension.DefaultQuotaName
	}

	return quotaName
}

// createDefaultQuotaIfNotPresent create DefaultQuotaGroup's CRD
func (g *Plugin) createDefaultQuotaIfNotPresent() {
	eqs, _ := g.elasticQuotaController.eqLister.ElasticQuotas("").List(labels.NewSelector())
	if len(eqs) == 0 {
		defaultElasticQuota := &schedulerv1alpha1.ElasticQuota{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "default",
				Labels: make(map[string]string),
			},
			Spec: schedulerv1alpha1.ElasticQuotaSpec{
				Max: g.pluginArgs.DefaultQuotaGroupMax,
			},
		}
		eq, err := g.handle.(versioned.Interface).SchedulingV1alpha1().ElasticQuotas("").Create(context.TODO(), defaultElasticQuota, metav1.CreateOptions{})
		if err != nil {
			klog.Errorf("err:%v", err)
		}
		klog.V(5).Infof("%v", eq)
	}
}

func getElasticQuotaSnapshotState(cycleState *framework.CycleState) (*SnapshotState, error) {
	c, err := cycleState.Read(SnapshotStateKey)
	if err != nil {
		return nil, fmt.Errorf("error reading %q from cycleState: %v", SnapshotStateKey, err)
	}

	s, ok := c.(*SnapshotState)
	if !ok {
		return nil, fmt.Errorf("%+v  convert to CapacityScheduling ElasticQuotaSnapshotState error", c)
	}
	return s, nil
}

func (g *Plugin) snapshotElasticQuota() *SnapshotState {
	g.RLock()
	defer g.RUnlock()

	elasticQuotaInfoDeepCopy := g.elasticQuotaInfos.clone()
	return &SnapshotState{
		elasticQuotaInfos: elasticQuotaInfoDeepCopy}
}

type SimpleElasticQuotaInfos map[string]*SimpleQuotaInfo

func NewSimpleElasticQuotaInfos() SimpleElasticQuotaInfos {
	return make(SimpleElasticQuotaInfos)
}

func (e SimpleElasticQuotaInfos) clone() SimpleElasticQuotaInfos {
	elasticQuotas := make(SimpleElasticQuotaInfos)
	for key, elasticQuotaInfo := range e {
		elasticQuotas[key] = elasticQuotaInfo.clone()
	}
	return elasticQuotas
}

// SimpleQuotaInfo stores Runtime/Used/Pod
type SimpleQuotaInfo struct {
	name    string
	runtime v1.ResourceList
	used    v1.ResourceList
	pods    sets.String
}

func NewSimpleQuotaInfo(name string) *SimpleQuotaInfo {
	elasticQuotaInfo := &SimpleQuotaInfo{
		name: name,
		pods: sets.NewString(),
	}
	return elasticQuotaInfo
}

func (e *SimpleQuotaInfo) clone() *SimpleQuotaInfo {
	newEQInfo := &SimpleQuotaInfo{
		name: e.name,
		pods: sets.NewString(),
	}
	if len(e.pods) > 0 {
		pods := e.pods.List()
		for _, pod := range pods {
			newEQInfo.pods.Insert(pod)
		}
	}

	if e.used != nil {
		newEQInfo.used = e.used.DeepCopy()
	}
	if e.runtime != nil {
		newEQInfo.runtime = e.runtime.DeepCopy()
	}
	return newEQInfo
}

func (e *SimpleQuotaInfo) setRuntime(runtime v1.ResourceList) {
	e.runtime = runtime
}

func (e *SimpleQuotaInfo) setUsed(used v1.ResourceList) {
	e.used = used
}

func (e *SimpleQuotaInfo) addPodIfNotPresent(pod *v1.Pod) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}

	if e.pods.Has(key) {
		return nil
	}

	e.pods.Insert(key)
	podRequest, _ := resource.PodRequestsAndLimits(pod)
	e.used = quotav1.Add(e.used, podRequest)
	return nil
}

func (e *SimpleQuotaInfo) deletePodIfPresent(pod *v1.Pod) error {
	key, err := framework.GetPodKey(pod)
	if err != nil {
		return err
	}

	if !e.pods.Has(key) {
		return nil
	}

	e.pods.Delete(key)
	podRequest, _ := resource.PodRequestsAndLimits(pod)
	e.used = quotav1.Subtract(e.used, podRequest)
	for _, resName := range quotav1.IsNegative(e.used) {
		e.used[resName] = *apiresource.NewQuantity(0, apiresource.DecimalSI)
	}
	return nil
}
