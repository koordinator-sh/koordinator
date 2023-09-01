package elasticquota

import (
	"reflect"

	"k8s.io/klog/v2"

	quotav1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

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
