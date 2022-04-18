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

package resmanager

import (
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/tools/cache"
)

var _ CacheExecutor = &ResourceUpdateExecutor{}

type CacheExecutor interface {
	UpdateByCache(resource ResourceUpdater) (updated bool, err error)
	UpdateWithoutErr(resource ResourceUpdater) (updated bool)
	Update(resource ResourceUpdater) error
	Run(stopCh <-chan struct{})
}

type ResourceUpdateExecutor struct {
	name               string
	resourceCache      *cache.Cache
	forceUpdateSeconds int

	locker *sync.Mutex
}

func NewResourceUpdateExecutor(name string, forceUpdateSeconds int) *ResourceUpdateExecutor {
	executor := &ResourceUpdateExecutor{
		name:               name,
		resourceCache:      cache.NewCacheDefault(),
		forceUpdateSeconds: forceUpdateSeconds,
		locker:             &sync.Mutex{},
	}

	return executor
}

func (rm *ResourceUpdateExecutor) Run(stopCh <-chan struct{}) {
	rm.resourceCache.Run(stopCh)
}

func (rm *ResourceUpdateExecutor) UpdateBatchByCache(resources ...ResourceUpdater) (updated bool) {
	rm.locker.Lock()
	defer rm.locker.Unlock()
	for _, resource := range resources {
		if !rm.needUpdate(resource) {
			continue
		}
		updated = true
		if rm.UpdateWithoutErr(resource) {
			resource.UpdateLastUpdateTimestamp(time.Now())
			err := rm.resourceCache.SetDefault(resource.Key(), resource)
			if err != nil {
				klog.Errorf("resourceCache.SetDefault fail! error: %v", err)
			}
		}
	}

	return
}

func (rm *ResourceUpdateExecutor) UpdateBatch(resources ...ResourceUpdater) {
	for _, resource := range resources {
		rm.UpdateWithoutErr(resource)
	}
}

func (rm *ResourceUpdateExecutor) UpdateByCache(resource ResourceUpdater) (updated bool, err error) {
	rm.locker.Lock()
	defer rm.locker.Unlock()

	if rm.needUpdate(resource) {
		updated = true
		err = rm.Update(resource)
		if err == nil {
			resource.UpdateLastUpdateTimestamp(time.Now())
			err1 := rm.resourceCache.SetDefault(resource.Key(), resource)
			if err1 != nil {
				klog.Errorf("resourceCache.SetDefault fail! error: %v", err1)
			}
		}
	}
	return
}

func (rm *ResourceUpdateExecutor) UpdateWithoutErr(resourceUpdater ResourceUpdater) bool {
	err := rm.Update(resourceUpdater)
	if err != nil {
		klog.Errorf("manager: %s, update resource failed, file: %s, value: %s, errMsg: %v", rm.name,
			resourceUpdater.Key(), resourceUpdater.Value(), err.Error())
		return false
	}
	return true
}

func (rm *ResourceUpdateExecutor) Update(resource ResourceUpdater) error {
	return resource.Update()
}

func (rm *ResourceUpdateExecutor) needUpdate(currentResource ResourceUpdater) bool {
	preResource, _ := rm.resourceCache.Get(currentResource.Key())
	if preResource == nil {
		klog.V(3).Infof("manager: %s, currentResource: %v, preResource: %v, need update", rm.name,
			currentResource, preResource)
		return true
	}
	preResourceUpdater := preResource.(ResourceUpdater)
	if currentResource.Value() != preResourceUpdater.Value() {
		klog.V(3).Infof("manager: %s, currentResource: %v, preResource: %v, need update", rm.name,
			currentResource, preResourceUpdater)
		return true
	}
	if time.Since(preResourceUpdater.GetLastUpdateTimestamp()) > time.Duration(rm.forceUpdateSeconds)*time.Second {
		klog.V(3).Infof("manager: %s, resource: %v, last update time(%v) is %v s ago, will update again",
			rm.name, preResourceUpdater, preResourceUpdater.GetLastUpdateTimestamp(), rm.forceUpdateSeconds)
		return true
	}
	return false
}
