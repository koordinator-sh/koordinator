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
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulerv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

// getPodAssociateQuotaName If pod's don't have the "quota-name" label, we will use the namespace to associate pod with quota
// group. If the plugin can't find the matched quota group, it will force the pod to associate with the "default-group".
func (g *Plugin) getPodAssociateQuotaName(pod *v1.Pod) string {
	quotaName := g.GetQuotaName(pod)
	if quotaName == "" {
		return ""
	}

	g.quotaToTreeMapLock.RLock()
	defer g.quotaToTreeMapLock.RUnlock()

	_, ok := g.quotaToTreeMap[quotaName]
	if ok {
		return quotaName
	}

	if k8sfeature.DefaultFeatureGate.Enabled(features.DisableDefaultQuota) {
		return ""
	}
	return extension.DefaultQuotaName
}

// getPodAssociateQuotaNameAndTreeID will return the quota and tree related the pod
// If pod's don't have the "quota-name" label, we will return the default quota and tree
// If pod has a quota label which not exists, we will also return the default quota and tree
func (g *Plugin) getPodAssociateQuotaNameAndTreeID(pod *v1.Pod) (string, string) {
	quotaName := g.GetQuotaName(pod)
	if quotaName == "" {
		return "", ""
	}

	g.quotaToTreeMapLock.RLock()
	defer g.quotaToTreeMapLock.RUnlock()

	treeID, ok := g.quotaToTreeMap[quotaName]
	if ok {
		return quotaName, treeID
	}

	if k8sfeature.DefaultFeatureGate.Enabled(features.DisableDefaultQuota) {
		return "", ""
	}
	return extension.DefaultQuotaName, treeID
}

func (g *Plugin) GetQuotaName(pod *v1.Pod) string {
	quotaName := extension.GetQuotaName(pod)
	if k8sfeature.DefaultFeatureGate.Enabled(features.DisableDefaultQuota) {
		return quotaName
	}

	if quotaName != "" {
		return quotaName
	}
	eq, err := g.quotaLister.ElasticQuotas(pod.Namespace).Get(pod.Namespace)
	if err == nil && eq != nil {
		return eq.Name
	} else if !errors.IsNotFound(err) {
		klog.Errorf("Failed to Get ElasticQuota %s, err: %v", pod.Namespace, err)
	}

	eqList, err := g.quotaInformer.GetIndexer().ByIndex("annotation.namespaces", pod.Namespace)
	if err != nil {
		return extension.DefaultQuotaName
	}

	for _, quota := range eqList {
		eq, ok := quota.(*schedulerv1alpha1.ElasticQuota)
		if !ok {
			continue
		}
		return eq.Name
	}

	return extension.DefaultQuotaName
}

// migrateDefaultQuotaGroupsPod traverse all the pods in DefaultQuotaGroup, if the pod's QuotaName is not DefaultQuotaName,
// then erase the pod from DefaultQuotaGroup, Request. If the pod is Running, update Used.
func (g *Plugin) migrateDefaultQuotaGroupsPod() {
	if k8sfeature.DefaultFeatureGate.Enabled(features.DisableDefaultQuota) {
		return
	}

	defaultQuotaInfo := g.groupQuotaManager.GetQuotaInfoByName(extension.DefaultQuotaName)
	for _, pod := range defaultQuotaInfo.GetPodCache() {
		quotaName, treeID := g.getPodAssociateQuotaNameAndTreeID(pod)
		if quotaName == extension.DefaultQuotaName {
			continue
		}
		curMgr := g.GetGroupQuotaManagerForTree(treeID)
		if curMgr == nil || curMgr.GetQuotaInfoByName(quotaName) == nil {
			continue
		}
		if curMgr.GetTreeID() != "" {
			// different tree.
			g.groupQuotaManager.OnPodDelete(extension.DefaultQuotaName, pod)
			curMgr.OnPodAdd(quotaName, pod)
		} else {
			// the same tree.
			curMgr.MigratePod(pod, extension.DefaultQuotaName, quotaName)
		}
	}
}

// migratePods if a quotaGroup is deleted, migrate its pods to defaultQuotaGroup
func (g *Plugin) migratePods(out, in string) {
	outQuota := g.groupQuotaManager.GetQuotaInfoByName(out)
	inQuota := g.groupQuotaManager.GetQuotaInfoByName(in)
	if outQuota != nil && inQuota != nil {
		for _, pod := range outQuota.GetPodCache() {
			g.groupQuotaManager.MigratePod(pod, out, in)
		}
	}
}

// createDefaultQuotaIfNotPresent create DefaultQuotaGroup's CRD
func (g *Plugin) createDefaultQuotaIfNotPresent() {
	eq, err := g.client.SchedulingV1alpha1().ElasticQuotas(g.pluginArgs.QuotaGroupNamespace).Get(context.TODO(), extension.DefaultQuotaName, metav1.GetOptions{ResourceVersion: "0"})
	if err == nil && eq != nil {
		klog.Infof("DefaultQuota already exists, skip create it.")
		return
	}
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("failed to get DefaultQuota, err: %v", err)
		return
	}

	defaultElasticQuota := &schedulerv1alpha1.ElasticQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:        extension.DefaultQuotaName,
			Namespace:   g.pluginArgs.QuotaGroupNamespace,
			Annotations: make(map[string]string),
		},
		Spec: schedulerv1alpha1.ElasticQuotaSpec{
			Max: g.pluginArgs.DefaultQuotaGroupMax.DeepCopy(),
		},
	}
	_, err = g.client.SchedulingV1alpha1().ElasticQuotas(g.pluginArgs.QuotaGroupNamespace).
		Create(context.TODO(), defaultElasticQuota, metav1.CreateOptions{})
	if err != nil {
		klog.Errorf("create default group fail, err:%v", err.Error())
		return
	}
	klog.Infof("create DefaultQuota successfully")
}

// defaultQuotaInfo and systemQuotaInfo are created once the groupQuotaManager is created, but we also want to see
// the used/request of the two quotaGroups, so we create the two quota's CRD if not present.
func (g *Plugin) createSystemQuotaIfNotPresent() {
	eq, err := g.client.SchedulingV1alpha1().ElasticQuotas(g.pluginArgs.QuotaGroupNamespace).Get(context.TODO(), extension.SystemQuotaName, metav1.GetOptions{ResourceVersion: "0"})
	if err == nil && eq != nil {
		klog.Infof("SystemQuota already exists, skip create it.")
		return
	}
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("failed to get SystemQuota, err: %v", err)
		return
	}

	systemElasticQuota := &schedulerv1alpha1.ElasticQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:        extension.SystemQuotaName,
			Namespace:   g.pluginArgs.QuotaGroupNamespace,
			Annotations: make(map[string]string),
		},
		Spec: schedulerv1alpha1.ElasticQuotaSpec{
			Max: g.pluginArgs.SystemQuotaGroupMax.DeepCopy(),
		},
	}
	_, err = g.client.SchedulingV1alpha1().ElasticQuotas(g.pluginArgs.QuotaGroupNamespace).
		Create(context.TODO(), systemElasticQuota, metav1.CreateOptions{})
	if err != nil {
		klog.Errorf("create system group fail, err:%v", err.Error())
		return
	}
	klog.Infof("create SystemQuota successfully")
}

// createRootQuotaIfNotPresent create RootQuotaGroup's CRD
func (g *Plugin) createRootQuotaIfNotPresent() {
	eq, err := g.client.SchedulingV1alpha1().ElasticQuotas(g.pluginArgs.QuotaGroupNamespace).Get(context.TODO(), extension.RootQuotaName, metav1.GetOptions{ResourceVersion: "0"})
	if err == nil && eq != nil {
		klog.Infof("RootQuota already exists, skip create it.")
		return
	}
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("failed to get RootQuota, err: %v", err)
		return
	}

	rootElasticQuota := &schedulerv1alpha1.ElasticQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:        extension.RootQuotaName,
			Namespace:   g.pluginArgs.QuotaGroupNamespace,
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
	}
	rootElasticQuota.Labels[extension.LabelQuotaIsParent] = "true"
	rootElasticQuota.Labels[extension.LabelAllowLentResource] = "false"
	rootElasticQuota.Labels[extension.LabelQuotaParent] = ""
	_, err = g.client.SchedulingV1alpha1().ElasticQuotas(g.pluginArgs.QuotaGroupNamespace).
		Create(context.TODO(), rootElasticQuota, metav1.CreateOptions{})
	if err != nil {
		klog.Errorf("create root group fail, err:%v", err.Error())
		return
	}
	klog.Infof("create RootQuota successfully")
}

func (g *Plugin) snapshotPostFilterState(quotaInfo *core.QuotaInfo, state *framework.CycleState) *PostFilterState {
	postFilterState := &PostFilterState{
		quotaInfo:          quotaInfo,
		used:               quotaInfo.GetUsed(),
		nonPreemptibleUsed: quotaInfo.GetNonPreemptibleUsed(),
		usedLimit:          g.getQuotaInfoUsedLimit(quotaInfo),
	}
	state.Write(postFilterKey, postFilterState)
	return postFilterState
}

func (g *Plugin) skipPostFilterState(state *framework.CycleState) {
	postFilterState := &PostFilterState{
		skip: true,
	}
	state.Write(postFilterKey, postFilterState)
}

func getPostFilterState(cycleState *framework.CycleState) (*PostFilterState, error) {
	c, err := cycleState.Read(postFilterKey)
	if err != nil {
		return nil, fmt.Errorf("error reading %q from cycleState: %v", postFilterKey, err)
	}

	s, ok := c.(*PostFilterState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to ElasticQuota.postFilterState error", c)
	}
	return s, nil
}

func (g *Plugin) checkQuotaRecursive(curQuotaName string, quotaNameTopo []string, podRequest v1.ResourceList) *framework.Status {
	quotaInfo := g.groupQuotaManager.GetQuotaInfoByName(curQuotaName)
	if quotaInfo == nil {
		return framework.NewStatus(framework.Error, fmt.Sprintf("Could not find the elasticQuota %v, quotaNameTopo: %v", curQuotaName, quotaNameTopo))
	}
	quotaUsed := quotaInfo.GetUsed()
	quotaUsedLimit := g.getQuotaInfoUsedLimit(quotaInfo)

	newUsed := quotav1.Mask(quotav1.Add(podRequest, quotaUsed), quotav1.ResourceNames(podRequest))
	if isLessEqual, exceedDimensions := quotav1.LessThanOrEqual(newUsed, quotaUsedLimit); !isLessEqual {
		return framework.NewStatus(framework.Unschedulable, fmt.Sprintf("Insufficient quotas, "+
			"quotaNameTopo: %v, runtime: %v, used: %v, pod's request: %v, exceedDimensions: %v", quotaNameTopo,
			printResourceList(quotaUsedLimit), printResourceList(quotaUsed), printResourceList(podRequest), exceedDimensions))
	}
	if quotaInfo.ParentName != extension.RootQuotaName {
		quotaNameTopo = append([]string{quotaInfo.ParentName}, quotaNameTopo...)
		return g.checkQuotaRecursive(quotaInfo.ParentName, quotaNameTopo, podRequest)
	}
	return framework.NewStatus(framework.Success, "")
}

func printResourceList(rl v1.ResourceList) string {
	if len(rl) == 0 {
		return "<empty>"
	}
	res := make([]string, 0)
	for k, v := range rl {
		tmp := string(k) + ":" + v.String()
		res = append(res, tmp)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i] < res[j]
	})
	return strings.Join(res, ",")
}

func (g *Plugin) getQuotaInfoUsedLimit(quotaInfo *core.QuotaInfo) v1.ResourceList {
	if g.pluginArgs.EnableRuntimeQuota {
		return quotaInfo.GetRuntime()
	}
	return quotaInfo.GetMax()
}
