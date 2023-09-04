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
	"reflect"

	"k8s.io/klog/v2"

	quotav1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

func (g *Plugin) GetProfileGroupQuotaManagers() []*core.GroupQuotaManager {
	g.quotaManagerLock.RLock()
	defer g.quotaManagerLock.RUnlock()

	managers := make([]*core.GroupQuotaManager, 0, len(g.profileGroupQuotaManagers))
	for _, mgr := range g.profileGroupQuotaManagers {
		managers = append(managers, mgr)
	}

	return managers
}

func (g *Plugin) GetGroupQuotaManager(profile string) *core.GroupQuotaManager {
	g.quotaManagerLock.RLock()
	defer g.quotaManagerLock.RUnlock()

	if profile == "" {
		// return the default groupQuotaManager
		return g.groupQuotaManager
	}

	return g.profileGroupQuotaManagers[profile]
}

func (g *Plugin) OnElasticQuotaProfileAdd(obj interface{}) {
	profile, ok := obj.(*quotav1alpha1.ElasticQuotaProfile)
	if !ok {
		return
	}
	if profile.DeletionTimestamp != nil {
		klog.V(5).Infof("OnElasticQuotaProfileAddFunc add:%v delete:%v", profile.Name, profile.DeletionTimestamp)
		return
	}

	g.quotaManagerLock.Lock()
	mgr, ok := g.profileGroupQuotaManagers[profile.Name]
	if !ok {
		mgr = core.NewGroupQuotaManager(g.pluginArgs.SystemQuotaGroupMax, g.pluginArgs.DefaultQuotaGroupMax, g.nodeLister, profile)
		g.profileGroupQuotaManagers[profile.Name] = mgr
		g.quotaManagerLock.Unlock()
	} else {
		g.quotaManagerLock.Unlock()
	}

	mgr.ResyncNodes()
}

func (g *Plugin) OnElasticQuotaProfileUpdate(oldObj, newObj interface{}) {
	newProfile := newObj.(*quotav1alpha1.ElasticQuotaProfile)
	oldProfile := oldObj.(*quotav1alpha1.ElasticQuotaProfile)

	if newProfile.ResourceVersion == oldProfile.ResourceVersion {
		return
	}

	g.quotaManagerLock.Lock()
	mgr, ok := g.profileGroupQuotaManagers[newProfile.Name]
	if !ok {
		mgr = core.NewGroupQuotaManager(g.pluginArgs.SystemQuotaGroupMax, g.pluginArgs.DefaultQuotaGroupMax, g.nodeLister, newProfile)
		g.profileGroupQuotaManagers[newProfile.Name] = mgr
		g.quotaManagerLock.Unlock()

		mgr.ResyncNodes()
	} else {
		g.quotaManagerLock.Unlock()
		// check node selector
		if !reflect.DeepEqual(newProfile.Spec.NodeSelector, oldProfile.Spec.NodeSelector) {
			mgr.SetElasticQuotaProfile(newProfile)
			mgr.ResyncNodes()
		}
	}
}

func (g *Plugin) OnElasticQuotaProfileDelete(obj interface{}) {
	profile, ok := obj.(*quotav1alpha1.ElasticQuotaProfile)
	if !ok {
		klog.Errorf("profile is nil")
		return
	}

	g.quotaManagerLock.Lock()
	defer g.quotaManagerLock.Unlock()

	_, ok = g.profileGroupQuotaManagers[profile.Name]
	if ok {
		delete(g.profileGroupQuotaManagers, profile.Name)
	}
}
