package core

import (
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	pglister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coescheduling"
)

type GangCache struct {
	lock       *sync.RWMutex
	gangItems  map[string]*Gang
	pluginArgs *schedulingconfig.GangArgs
	podLister  listerv1.PodLister
	pgLister   pglister.PodGroupLister
}

func NewGangCache(args *schedulingconfig.GangArgs, podLister listerv1.PodLister, pgLister pglister.PodGroupLister) *GangCache {
	return &GangCache{
		gangItems:  make(map[string]*Gang),
		lock:       new(sync.RWMutex),
		pluginArgs: args,
		podLister:  podLister,
		pgLister:   pgLister,
	}
}

func (gangCache *GangCache) createGangToCache(gangId string) *Gang {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	if gang, exist := gangCache.gangItems[gangId]; exist {
		return gang
	}

	gang := NewGang(gangId)
	gangCache.gangItems[gangId] = gang
	return gang
}

func (gangCache *GangCache) getGangFromCache(gangId string) *Gang {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()

	return gangCache.gangItems[gangId]
}

func (gangCache *GangCache) deleteGangFromCache(gangId string) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	delete(gangCache.gangItems, gangId)
}
func (gangCache *GangCache) getGangItems() map[string]*Gang {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	return gangCache.gangItems
}

func (gangCache *GangCache) RecoverGangCache() {
	// recover podGroup First
	pgLister := gangCache.pgLister
	pgList, err := pgLister.List(labels.NewSelector())
	if err != nil {
		klog.Errorf("RecoverGangCache podGroupList List error: %v", err.Error())
	}
	for _, podGroup := range pgList {
		gangCache.onPodGroupAdd(podGroup)

	}
	// recover gang from pod
	podLister := gangCache.podLister
	podsList, err := podLister.List(labels.NewSelector())
	if err != nil {
		klog.Errorf("RecoverGangCache podsList List error: %v", err.Error())
	}
	for _, pod := range podsList {
		gangCache.onPodAdd(pod)

		if pod.Spec.NodeName != "" {
			gangName := coescheduling.GetGangNameByPod(pod)
			if gangName == "" {
				continue
			}
			gangId := coescheduling.GetNamespaceSplicingName(pod.Namespace,
				gangName)
			gang := gangCache.getGangFromCache(gangId)
			if gang == nil {
				klog.Errorf("Didn't find gang when recover by pod, pod:%v, gang: %v", pod.Name, gangId)
				continue
			}
			gang.addBoundPod(pod)
			gang.setResourceSatisfied()
		}
	}

}

func (gangCache *GangCache) onPodAdd(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}

	gangName := coescheduling.GetGangNameByPod(pod)
	if gangName == "" {
		return
	}
	var gang *Gang
	// the gang is created in either CRD way or Annotation way
	if _, exist := pod.Labels[v1alpha1.PodGroupLabel]; exist {
		gangName := pod.Labels[v1alpha1.PodGroupLabel]
		gangNamespace := pod.Namespace
		gangId := coescheduling.GetNamespaceSplicingName(gangNamespace, gangName)
		gang = gangCache.getGangFromCache(gangId)
		//todo : new case,need UT
		if gang == nil {
			gang = gangCache.createGangToCache(gangId)
			klog.Infof("Pod comes earlier than PodGroup,so we create gang by pod on add, gangName: %v", gangId)
		}
	} else {
		gangNamespace := pod.Namespace
		gangName := pod.Annotations[extension.AnnotationGangName]
		gangId := coescheduling.GetNamespaceSplicingName(gangNamespace, gangName)
		gang = gangCache.getGangFromCache(gangId)
		if gang == nil {
			gang = gangCache.createGangToCache(gangId)
			klog.Infof("Create gang by pod on add, gangName: %v", gangId)
		}
		gang.tryInitByPodConfig(pod, gangCache.pluginArgs)
	}
	gang.setChild(pod)
	gang.setChildScheduleCycle(pod, 0)
}

func (gangCache *GangCache) onPodUpdate(oldObj interface{}, newObj interface{}) {
}

func (gangCache *GangCache) onPodDelete(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}
	gangName := coescheduling.GetGangNameByPod(pod)
	if gangName == "" {
		return
	}

	gangNamespace := pod.Namespace
	gangId := coescheduling.GetNamespaceSplicingName(gangNamespace, gangName)
	gang := gangCache.getGangFromCache(gangId)
	if gang == nil {
		return
	}

	shouldDeleteGang := gang.deletePod(pod)
	if shouldDeleteGang {
		gangCache.deleteGangFromCache(gangId)
		klog.Infof("Delete gang from gang cache, gangName: %v", gangId)
	}
}

func (gangCache *GangCache) onPodGroupAdd(obj interface{}) {
	pg, ok := obj.(*v1alpha1.PodGroup)
	if !ok {
		return
	}
	gangNamespace := pg.Namespace
	gangName := pg.Name

	gangId := coescheduling.GetNamespaceSplicingName(gangNamespace, gangName)
	gang := gangCache.getGangFromCache(gangId)
	if gang == nil {
		gang = gangCache.createGangToCache(gangId)
		klog.Infof("Create gang by podGroup on add, gangName: %v", gangId)
	}
	gang.tryInitByPodGroup(pg, gangCache.pluginArgs)
}

func (gangCache *GangCache) onPodGroupUpdate(oldObj interface{}, newObj interface{}) {
}

func (gangCache *GangCache) onPodGroupDelete(obj interface{}) {
	pg, ok := obj.(*v1alpha1.PodGroup)
	if !ok {
		return
	}
	gangNamespace := pg.Namespace
	gangName := pg.Name

	gangId := coescheduling.GetNamespaceSplicingName(gangNamespace, gangName)
	gang := gangCache.getGangFromCache(gangId)
	if gang == nil {
		return
	}
	gangCache.deleteGangFromCache(gangId)
	klog.Infof("Delete gang from gang cache, gangName: %v", gang.Name)
}
