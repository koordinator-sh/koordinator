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
	"encoding/json"
	"time"

	corev1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulerv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

func (g *Plugin) OnQuotaAdd(obj interface{}) {
	quota, ok := obj.(*schedulerv1alpha1.ElasticQuota)
	if !ok {
		klog.Errorf("quota is nil")
		return
	}

	if quota.DeletionTimestamp != nil {
		klog.Errorf("quota is deleting: %v", quota.Name)
		return
	}

	klog.V(5).Infof("OnQuotaAddFunc add quota: %v", quota.Name)
	mgr := g.GetOrCreateGroupQuotaManagerForTree(quota.Labels[extension.LabelQuotaTreeID])
	treeID := mgr.GetTreeID()
	g.updateQuotaToTreeMap(quota.Name, treeID)

	g.handlerQuotaWhenRoot(quota, mgr, false)

	oldQuotaInfo := mgr.GetQuotaInfoByName(quota.Name)
	if oldQuotaInfo != nil && quota.Name != extension.DefaultQuotaName && quota.Name != extension.SystemQuotaName {
		return
	}

	err := mgr.UpdateQuota(quota, false)
	if err != nil {
		klog.V(5).Infof("OnQuotaAddFunc failed: %v, tree: %v, err: %v", quota.Name, treeID, err)
		return
	}
	klog.V(5).Infof("OnQuotaAddFunc success: %v, tree: %v", quota.Name, treeID)
}

func (g *Plugin) OnQuotaUpdate(oldObj, newObj interface{}) {
	newQuota := newObj.(*schedulerv1alpha1.ElasticQuota)

	if newQuota.DeletionTimestamp != nil {
		klog.Warningf("update quota warning, update is deleting: %v", newQuota.Name)
		return
	}

	// forbidden change quota tree.
	klog.V(5).Infof("OnQuotaUpdateFunc update quota: %v", newQuota.Name)
	mgr := g.GetOrCreateGroupQuotaManagerForTree(newQuota.Labels[extension.LabelQuotaTreeID])
	treeID := mgr.GetTreeID()
	g.updateQuotaToTreeMap(newQuota.Name, treeID)

	g.handlerQuotaWhenRoot(newQuota, mgr, false)

	err := mgr.UpdateQuota(newQuota, false)
	if err != nil {
		klog.V(5).Infof("OnQuotaUpdateFunc failed: %v, tree: %v, err: %v", newQuota.Name, treeID, err)
		return
	}
	klog.V(5).Infof("OnQuotaUpdateFunc success: %v, tree: %v", newQuota.Name, treeID)
}

// OnQuotaDelete if a quotaGroup is deleted, the pods should migrate to defaultQuotaGroup.
func (g *Plugin) OnQuotaDelete(obj interface{}) {
	quota := obj.(*schedulerv1alpha1.ElasticQuota)
	if quota == nil {
		klog.Errorf("quota is nil")
		return
	}

	klog.V(5).Infof("OnQuotaDeleteFunc delete quota: %v", quota.Name)
	g.deleteQuotaToTreeMap(quota.Name)
	mgr := g.GetGroupQuotaManagerForTree(quota.Labels[extension.LabelQuotaTreeID])
	if mgr == nil {
		return
	}
	treeID := mgr.GetTreeID()
	err := mgr.UpdateQuota(quota, true)
	if err != nil {
		klog.Errorf("OnQuotaDeleteFunc failed: %v, tree: %v, err: %v", quota.Name, treeID, err)
		return
	}

	g.handlerQuotaWhenRoot(quota, mgr, true)

	klog.V(5).Infof("OnQuotaDeleteFunc failed: %v, tree: %v", quota.Name, treeID)

}

func (g *Plugin) ReplaceQuotas(objs []interface{}) error {
	quotas := make([]*schedulerv1alpha1.ElasticQuota, 0, len(objs))
	for _, obj := range objs {
		quota := obj.(*schedulerv1alpha1.ElasticQuota)
		quotas = append(quotas, quota)
	}

	start := time.Now()
	defer func() {
		klog.Infof("ReplaceQuotas replace %v quotas take %v", len(quotas), time.Since(start))
	}()

	g.groupQuotaManagersForQuotaTree = make(map[string]*core.GroupQuotaManager)
	g.groupQuotaManager = core.NewGroupQuotaManager("", g.pluginArgs.SystemQuotaGroupMax, g.pluginArgs.DefaultQuotaGroupMax)
	g.quotaToTreeMap = make(map[string]string)
	g.quotaToTreeMap[extension.DefaultQuotaName] = ""
	g.quotaToTreeMap[extension.SystemQuotaName] = ""

	for _, quota := range quotas {
		if quota.DeletionTimestamp != nil {
			continue
		}
		mgr := g.GetOrCreateGroupQuotaManagerForTree(quota.Labels[extension.LabelQuotaTreeID])
		treeID := mgr.GetTreeID()
		g.updateQuotaToTreeMap(quota.Name, treeID)
		g.handlerQuotaWhenRoot(quota, mgr, false)
		mgr.UpdateQuotaInfo(quota)
	}

	g.groupQuotaManager.ResetQuota()
	for _, mgr := range g.groupQuotaManagersForQuotaTree {
		mgr.ResetQuota()
	}

	return nil
}

func (g *Plugin) GetQuotaSummary(quotaName string, includePods bool) (*core.QuotaInfoSummary, bool) {
	mgr := g.GetGroupQuotaManagerForQuota(quotaName)
	return mgr.GetQuotaSummary(quotaName, includePods)
}

func (g *Plugin) GetQuotaSummaries(tree string, includePods bool) map[string]*core.QuotaInfoSummary {
	summaries := make(map[string]*core.QuotaInfoSummary)

	managers := g.ListGroupQuotaManagersForQuotaTree()
	for _, mgr := range managers {
		if tree != "" && mgr.GetTreeID() != tree {
			continue
		}
		for quotaName, summary := range mgr.GetQuotaSummaries(includePods) {
			summaries[quotaName] = summary
		}
	}

	if g.groupQuotaManager.GetTreeID() == tree {
		for quotaName, summary := range g.groupQuotaManager.GetQuotaSummaries(includePods) {
			summaries[quotaName] = summary
		}
	}

	return summaries
}

func (g *Plugin) GetOrCreateGroupQuotaManagerForTree(treeID string) *core.GroupQuotaManager {
	if !k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.MultiQuotaTree) {
		// return the default manager
		return g.groupQuotaManager
	}
	if treeID == "" {
		return g.groupQuotaManager
	}

	// read lock
	g.quotaManagerLock.RLock()
	mgr, ok := g.groupQuotaManagersForQuotaTree[treeID]
	if ok {
		g.quotaManagerLock.RUnlock()
		return mgr
	}
	g.quotaManagerLock.RUnlock()

	// write lock
	g.quotaManagerLock.Lock()
	mgr, ok = g.groupQuotaManagersForQuotaTree[treeID]
	if !ok {
		mgr = core.NewGroupQuotaManager(treeID, g.pluginArgs.SystemQuotaGroupMax, g.pluginArgs.DefaultQuotaGroupMax)
		g.groupQuotaManagersForQuotaTree[treeID] = mgr
	}
	g.quotaManagerLock.Unlock()
	return mgr
}

func (g *Plugin) GetGroupQuotaManagerForTree(treeID string) *core.GroupQuotaManager {
	if !k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.MultiQuotaTree) {
		return g.groupQuotaManager
	}
	if treeID == "" {
		return g.groupQuotaManager
	}

	g.quotaManagerLock.RLock()
	defer g.quotaManagerLock.RUnlock()

	return g.groupQuotaManagersForQuotaTree[treeID]
}

func (g *Plugin) GetGroupQuotaManagerForQuota(quotaName string) *core.GroupQuotaManager {
	if !k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.MultiQuotaTree) {
		return g.groupQuotaManager
	}

	g.quotaToTreeMapLock.RLock()
	treeID := g.quotaToTreeMap[quotaName]
	g.quotaToTreeMapLock.RUnlock()
	if treeID == "" {
		return g.groupQuotaManager
	}

	g.quotaManagerLock.RLock()
	mgr := g.groupQuotaManagersForQuotaTree[treeID]
	g.quotaManagerLock.RUnlock()
	if mgr == nil {
		return g.groupQuotaManager
	}
	return mgr
}

func (g *Plugin) ListGroupQuotaManagersForQuotaTree() []*core.GroupQuotaManager {
	g.quotaManagerLock.RLock()
	defer g.quotaManagerLock.RUnlock()

	managers := make([]*core.GroupQuotaManager, 0, len(g.groupQuotaManagersForQuotaTree))
	for _, mgr := range g.groupQuotaManagersForQuotaTree {
		managers = append(managers, mgr)
	}

	return managers
}

func (g *Plugin) updateQuotaToTreeMap(quota, tree string) {
	g.quotaToTreeMapLock.RLock()
	_, ok := g.quotaToTreeMap[quota]
	if ok {
		g.quotaToTreeMapLock.RUnlock()
		return
	}
	g.quotaToTreeMapLock.RUnlock()

	g.quotaToTreeMapLock.Lock()
	_, ok = g.quotaToTreeMap[quota]
	if !ok {
		g.quotaToTreeMap[quota] = tree
	}
	g.quotaToTreeMapLock.Unlock()
}

func (g *Plugin) deleteQuotaToTreeMap(quota string) {
	g.quotaToTreeMapLock.Lock()
	delete(g.quotaToTreeMap, quota)
	g.quotaToTreeMapLock.Unlock()
}

// handlerQuotaForRoot will update quota tree total resource when the quota is root quota and enable MultiQuotaTree
func (g *Plugin) handlerQuotaWhenRoot(quota *schedulerv1alpha1.ElasticQuota, mgr *core.GroupQuotaManager, isDelete bool) {
	if !k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.MultiQuotaTree) ||
		quota.Labels[extension.LabelQuotaIsRoot] != "true" || mgr.GetTreeID() == "" {
		return
	}

	totalResource, ok := getTotalResource(quota)
	if ok {
		var delta corev1.ResourceList
		if isDelete {
			delta = quotav1.Subtract(corev1.ResourceList{}, totalResource)
			g.quotaManagerLock.Lock()
			delete(g.groupQuotaManagersForQuotaTree, mgr.GetTreeID())
			g.quotaManagerLock.Unlock()
		} else {
			delta = mgr.SetTotalResourceForTree(totalResource)
		}

		if !quotav1.IsZero(delta) && quota.Labels[extension.LabelQuotaIgnoreDefaultTree] != "true" {
			// decrease the default GroupQuotaManager resource
			deltaForDefault := quotav1.Subtract(corev1.ResourceList{}, delta)
			g.groupQuotaManager.UpdateClusterTotalResource(deltaForDefault)
		}
	}
}

func getTotalResource(quota *schedulerv1alpha1.ElasticQuota) (corev1.ResourceList, bool) {
	var total corev1.ResourceList

	raw := quota.Annotations[extension.AnnotationTotalResource]
	if raw == "" {
		return total, false
	}

	err := json.Unmarshal([]byte(raw), &total)
	if err != nil {
		klog.Errorf("failed unmarshal total resource for %v, err: %v", quota.Name, err)
		return total, false
	}

	return total, true
}
