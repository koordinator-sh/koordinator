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

package core

import (
	"sync"

	v1 "k8s.io/api/core/v1"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	pgclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"
	pglister "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
	koordutil "github.com/koordinator-sh/koordinator/pkg/util"
)

type GangCache struct {
	lock             *sync.RWMutex
	gangItems        map[string]*Gang
	gangGroupInfoMap map[string]*GangGroupInfo
	pluginArgs       *config.CoschedulingArgs
	podLister        listerv1.PodLister
	pgLister         pglister.PodGroupLister
	pgClient         pgclientset.Interface
}

func NewGangCache(args *config.CoschedulingArgs, podLister listerv1.PodLister, pgLister pglister.PodGroupLister, client pgclientset.Interface) *GangCache {
	return &GangCache{
		gangItems:        make(map[string]*Gang),
		gangGroupInfoMap: make(map[string]*GangGroupInfo),
		lock:             new(sync.RWMutex),
		pluginArgs:       args,
		podLister:        podLister,
		pgLister:         pgLister,
		pgClient:         client,
	}
}

func (gangCache *GangCache) getGangGroupInfo(gangGroupId string, gangGroup []string, createIfNotExist bool) *GangGroupInfo {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	var gangGroupInfo *GangGroupInfo
	if gangCache.gangGroupInfoMap[gangGroupId] == nil {
		if createIfNotExist {
			gangGroupInfo = NewGangGroupInfo(gangGroupId, gangGroup)
			gangGroupInfo.SetInitialized()
			gangCache.gangGroupInfoMap[gangGroupId] = gangGroupInfo
			klog.Infof("add gangGroupInfo to cache, gangGroupId: %v", gangGroupId)
		}
	} else {
		gangGroupInfo = gangCache.gangGroupInfoMap[gangGroupId]
	}

	return gangGroupInfo
}

func (gangCache *GangCache) deleteGangGroupInfo(gangGroupId string) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	delete(gangCache.gangGroupInfoMap, gangGroupId)
	klog.Infof("delete gangGroupInfo from cache, gangGroupId: %v", gangGroupId)
}

func (gangCache *GangCache) getGangFromCacheByGangId(gangId string, createIfNotExist bool) *Gang {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	gang := gangCache.gangItems[gangId]
	if gang == nil && createIfNotExist {
		gang = NewGang(gangId)
		gangCache.gangItems[gangId] = gang
		klog.Infof("getGangFromCache create new gang, gang: %v", gangId)
	}
	return gang
}

func (gangCache *GangCache) getAllGangsFromCache() map[string]*Gang {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()

	result := make(map[string]*Gang)
	for gangId, gang := range gangCache.gangItems {
		result[gangId] = gang
	}

	return result
}

func (gangCache *GangCache) deleteGangFromCacheByGangId(gangId string) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	delete(gangCache.gangItems, gangId)
	klog.Infof("delete gang from cache, gang: %v", gangId)
}

func (gangCache *GangCache) onPodAdd(obj interface{}) {
	gangCache.onPodAddInternal(obj, "create")
}

func (gangCache *GangCache) onPodAddInternal(obj interface{}, action string) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}

	gangName := util.GetGangNameByPod(pod)
	if gangName == "" {
		return
	}

	gangNamespace := pod.Namespace
	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, true)

	// the gang is created in Annotation way
	if pod.Labels[v1alpha1.PodGroupLabel] == "" {
		gang.tryInitByPodConfig(pod, gangCache.pluginArgs)

		gangGroup := gang.getGangGroup()
		gangGroupId := util.GetGangGroupId(gangGroup)
		gangGroupInfo := gangCache.getGangGroupInfo(gangGroupId, gangGroup, true)
		gang.SetGangGroupInfo(gangGroupInfo)
		gang.initPodLastScheduleTime(pod)
	} else {
		//only podGroup added then can initPodLastScheduleTime
		gangGroup := gang.getGangGroup()
		gangGroupId := util.GetGangGroupId(gangGroup)
		gangGroupInfo := gangCache.getGangGroupInfo(gangGroupId, gangGroup, false)
		if gangGroupInfo != nil {
			gang.initPodLastScheduleTime(pod)
		}
	}

	gang.setChild(pod)
	if pod.Spec.NodeName != "" {
		gang.addBoundPod(pod)
		gang.setResourceSatisfied()
	}

	klog.Infof("watch pod %v, Name:%v, pgLabel:%v", action, pod.Name, pod.Labels[v1alpha1.PodGroupLabel])
}

func (gangCache *GangCache) onPodUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*v1.Pod)
	if !ok {
		return
	}

	gangName := util.GetGangNameByPod(pod)
	if gangName == "" {
		return
	}

	if koordutil.IsPodTerminated(pod) {
		return
	}

	gangCache.onPodAddInternal(newObj, "update")
}

func (gangCache *GangCache) onPodDelete(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}
	gangName := util.GetGangNameByPod(pod)
	if gangName == "" {
		return
	}

	gangNamespace := pod.Namespace
	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		return
	}

	shouldDeleteGang := gang.deletePod(pod)
	if shouldDeleteGang {
		gangCache.deleteGangFromCacheByGangId(gangId)

		allGangDeleted := true
		for _, gangId := range gang.GangGroup {
			if gangCache.getGangFromCacheByGangId(gangId, false) != nil {
				allGangDeleted = false
				break
			}
		}
		if allGangDeleted {
			gangCache.deleteGangGroupInfo(gang.GangGroupInfo.GangGroupId)
		}
	}

	klog.Infof("watch pod deleted, Name:%v, pgLabel:%v", pod.Name, pod.Labels[v1alpha1.PodGroupLabel])
}

func (gangCache *GangCache) onPodGroupAdd(obj interface{}) {
	pg, ok := obj.(*v1alpha1.PodGroup)
	if !ok {
		return
	}
	gangNamespace := pg.Namespace
	gangName := pg.Name

	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, true)
	gang.tryInitByPodGroup(pg, gangCache.pluginArgs)

	gangGroup := gang.getGangGroup()
	gangGroupId := util.GetGangGroupId(gangGroup)
	gangGroupInfo := gangCache.getGangGroupInfo(gangGroupId, gangGroup, true)
	gang.SetGangGroupInfo(gangGroupInfo)
	//reset already connected pods lastScheduleTime
	gang.initAllChildrenPodLastScheduleTime()

	klog.Infof("watch podGroup created, Name:%v", pg.Name)
}

func (gangCache *GangCache) onPodGroupUpdate(oldObj interface{}, newObj interface{}) {
	pg, ok := newObj.(*v1alpha1.PodGroup)
	if !ok {
		return
	}
	gangNamespace := pg.Namespace
	gangName := pg.Name

	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		klog.Errorf("Gang object isn't exist when got Update Event")
		return
	}
	gang.tryInitByPodGroup(pg, gangCache.pluginArgs)

	gangGroup := gang.getGangGroup()
	gangGroupId := util.GetGangGroupId(gangGroup)
	gangGroupInfo := gangCache.getGangGroupInfo(gangGroupId, gangGroup, true)
	gang.SetGangGroupInfo(gangGroupInfo)
}

func (gangCache *GangCache) onPodGroupDelete(obj interface{}) {
	pg, ok := obj.(*v1alpha1.PodGroup)
	if !ok {
		return
	}
	gangNamespace := pg.Namespace
	gangName := pg.Name

	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		return
	}
	gangCache.deleteGangFromCacheByGangId(gangId)

	allGangDeleted := true
	for _, gangId := range gang.GangGroup {
		if gangCache.getGangFromCacheByGangId(gangId, false) != nil {
			allGangDeleted = false
			break
		}
	}
	if allGangDeleted {
		gangCache.deleteGangGroupInfo(gang.GangGroupInfo.GangGroupId)
	}

	klog.Infof("watch podGroup deleted, Name:%v", pg.Name)
}
