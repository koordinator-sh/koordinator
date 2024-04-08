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
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	pgclientset "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned"
	pglister "sigs.k8s.io/scheduler-plugins/pkg/generated/listers/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
	koordutil "github.com/koordinator-sh/koordinator/pkg/util"
)

type GangCache struct {
	lock             *sync.RWMutex
	gangItems        map[string]*Gang
	pluginArgs       *config.CoschedulingArgs
	podLister        listerv1.PodLister
	pgLister         pglister.PodGroupLister
	pgClient         pgclientset.Interface
	podScheduleInfos map[types.UID]*PodScheduleInfo
}

type PodScheduleInfo struct {
	lock                sync.RWMutex
	lastScheduleTime    time.Time
	alreadyBeenRejected bool
}

func (p *PodScheduleInfo) SetLastScheduleTime(lastScheduleTime time.Time) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.lastScheduleTime = lastScheduleTime
}

func (p *PodScheduleInfo) SetAlreadyBeenRejected(rejected bool) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.alreadyBeenRejected = rejected
}

func (p *PodScheduleInfo) GetAlreadyBeenRejected() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.alreadyBeenRejected
}

func (p *PodScheduleInfo) GetLastScheduleTime() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.lastScheduleTime
}

func NewGangCache(args *config.CoschedulingArgs, podLister listerv1.PodLister, pgLister pglister.PodGroupLister, client pgclientset.Interface) *GangCache {
	return &GangCache{
		gangItems:        make(map[string]*Gang),
		lock:             new(sync.RWMutex),
		pluginArgs:       args,
		podLister:        podLister,
		pgLister:         pgLister,
		pgClient:         client,
		podScheduleInfos: make(map[types.UID]*PodScheduleInfo),
	}
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

func (gangCache *GangCache) getPodScheduleInfo(podUID types.UID, createIfNotExist bool) *PodScheduleInfo {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	podScheduleInfo := gangCache.podScheduleInfos[podUID]
	if podScheduleInfo == nil && createIfNotExist {
		podScheduleInfo = &PodScheduleInfo{}
		gangCache.podScheduleInfos[podUID] = podScheduleInfo
	}
	return podScheduleInfo
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
	}
	gang.setChild(pod)
	if pod.Spec.NodeName != "" {
		gang.addBoundPod(pod)
		gang.setResourceSatisfied()
		gangCache.deletePodScheduleInfo(pod)
	}
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

	gangCache.onPodAdd(newObj)
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
	gangCache.deletePodScheduleInfo(pod)

	gangNamespace := pod.Namespace
	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		return
	}

	shouldDeleteGang := gang.deletePod(pod)
	if shouldDeleteGang {
		gangCache.deleteGangFromCacheByGangId(gangId)
	}
}

func (gangCache *GangCache) deletePodScheduleInfo(pod *v1.Pod) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	delete(gangCache.podScheduleInfos, pod.UID)
	klog.Infof("delete podScheduleInfo from cache, pod: %s/%s/%v", pod.Namespace, pod.Name, pod.UID)
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
}
