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

package resourceexecutor

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util/cache"
)

var _ ResourceUpdateExecutor = &ResourceUpdateExecutorImpl{}

type ResourceUpdateExecutor interface {
	Update(cacheable bool, updater ResourceUpdater) (updated bool, err error)
	UpdateBatch(cacheable bool, updaters ...ResourceUpdater)
	// LeveledUpdateBatch is to cacheable update resources by the order of resources' level.
	// For cgroup interfaces like `cpuset.cpus` and `memory.min`, reconciliation from top to bottom should keep the
	// upper value larger/broader than the lower. Thus a Leveled updater is implemented as follows:
	// 1. update batch of cgroup resources group by cgroup interface, i.e. cgroup filename.
	// 2. update each cgroup resource by the order of layers: firstly update resources from upper to lower by merging
	//    the new value with old value; then update resources from lower to upper with the new value.
	LeveledUpdateBatch(updaters [][]ResourceUpdater)
	Run(stopCh <-chan struct{})
}

type ResourceUpdateExecutorImpl struct {
	LeveledUpdateLock sync.Mutex
	ResourceCache     *cache.Cache
	Config            *Config

	onceRun   sync.Once
	gcStarted bool
}

var singleton = &ResourceUpdateExecutorImpl{
	ResourceCache: cache.NewCacheDefault(),
	Config:        Conf,
}

func NewResourceUpdateExecutor() ResourceUpdateExecutor {
	return singleton
}

// Update updates the resources with the given cacheable attribute with the cacheable attribute directly.
func (e *ResourceUpdateExecutorImpl) Update(cacheable bool, resource ResourceUpdater) (bool, error) {
	if cacheable {
		if !e.gcStarted {
			klog.V(5).Info("failed to cacheable update resources, err: cache GC is not started")
			return false, fmt.Errorf("cache GC is not started")
		}
		return e.updateByCache(resource)
	}
	return true, e.update(resource)
}

// UpdateBatch updates a batch of resources with the given cacheable attribute.
// TODO: merge and resolve conflicts of batch updates from multiple callers.
func (e *ResourceUpdateExecutorImpl) UpdateBatch(cacheable bool, updaters ...ResourceUpdater) {
	failures := 0
	if cacheable {
		if !e.gcStarted {
			klog.Error("failed to cacheable update resources, err: cache GC is not started")
			return
		}

		for _, updater := range updaters {
			isUpdated, err := e.updateByCache(updater)
			if err != nil {
				failures++
				klog.V(4).Infof("failed to cacheable update resource %s to %v, isUpdated %v, err: %v",
					updater.Key(), updater.Value(), isUpdated, err)
				continue
			}

			klog.V(5).Infof("successfully cacheable update resource %s to %v, isUpdated %v",
				updater.Key(), updater.Value(), isUpdated)
		}
	} else {
		for _, updater := range updaters {
			err := e.update(updater)
			if err != nil {
				failures++
				klog.V(4).Infof("failed to update resource %s to %v, err: %v", updater.Key(), updater.Value(), err)
				continue
			}

			klog.V(5).Infof("successfully update resource %s to %v", updater.Key(), updater.Value())
		}
	}
	klog.V(6).Infof("finished batch updating resources, isCacheable %v, total %v, failures %v",
		cacheable, len(updaters), failures)
}

func (e *ResourceUpdateExecutorImpl) LeveledUpdateBatch(updaters [][]ResourceUpdater) {
	e.LeveledUpdateLock.Lock()
	defer e.LeveledUpdateLock.Unlock()
	if !e.gcStarted {
		klog.Error("failed to cacheable level update resources, err: cache GC is not started")
		return
	}

	var err error
	skipMerge := map[string]bool{}
	for i := 0; i < len(updaters); i++ {
		for _, updater := range updaters[i] {
			if !e.needUpdate(updater) {
				continue
			}

			mergedUpdater, err := updater.MergeUpdate()
			if err != nil && e.isUpdateErrIgnored(err) {
				klog.V(5).Infof("failed to merge update resource %s to %v, ignored err: %v",
					updater.Key(), updater.Value(), err)
				continue
			}
			if err != nil {
				klog.V(4).Infof("failed to merge update resource %s to %v, err: %v",
					updater.Key(), updater.Value(), err)
				continue
			}
			klog.V(5).Infof("successfully merge update resource %s to %v", updater.Key(), updater.Value())

			if mergedUpdater == nil {
				skipMerge[updater.Key()] = true
			} else {
				updater = mergedUpdater
			}

			updater.UpdateLastUpdateTimestamp(time.Now())
			err = e.ResourceCache.SetDefault(updater.Key(), updater)
			if err != nil {
				klog.V(4).Infof("failed to SetDefault in resourceCache for resource %s, err: %v",
					updater.Key(), err)
			}
		}
	}

	for i := len(updaters) - 1; i >= 0; i-- {
		for _, updater := range updaters[i] {
			if !e.needUpdate(updater) {
				continue
			}

			// skip update twice for resources specified no merge
			if skipMerge[updater.Key()] {
				klog.V(6).Infof("skip update resource %s since it should skip the merge", updater.Key())
				continue
			}
			err = updater.update()
			if err != nil && e.isUpdateErrIgnored(err) {
				klog.V(5).Infof("failed to update resource %s to %v, ignored err: %v", updater.Key(), updater.Value(), err)
				continue
			}
			if err != nil {
				klog.V(4).Infof("failed update resource %s, err: %v", updater.Key(), err)
				continue
			}
			klog.V(6).Infof("successfully update resource %s to %v", updater.Key(), updater.Value())

			updater.UpdateLastUpdateTimestamp(time.Now())
			err = e.ResourceCache.SetDefault(updater.Key(), updater)
			if err != nil {
				klog.V(4).Infof("failed to SetDefault in resourceCache for resource %s, err: %v",
					updater.Key(), err)
			}
		}
	}
}

// Run runs the ResourceUpdateExecutor.
func (e *ResourceUpdateExecutorImpl) Run(stopCh <-chan struct{}) {
	e.onceRun.Do(func() {
		e.run(stopCh)
	})
}

func (e *ResourceUpdateExecutorImpl) run(stopCh <-chan struct{}) {
	_ = e.ResourceCache.Run(stopCh)
	klog.V(4).Info("starting ResourceUpdateExecutor successfully")
	e.gcStarted = true
}

func (e *ResourceUpdateExecutorImpl) needUpdate(updater ResourceUpdater) bool {
	preResource, _ := e.ResourceCache.Get(updater.Key())
	if preResource == nil {
		klog.V(5).Infof("check for resource %s: pre is nil, need update", updater.Key())
		return true
	}
	preResourceUpdater := preResource.(ResourceUpdater)
	if updater.Value() != preResourceUpdater.Value() {
		klog.V(5).Infof("check for resource %s: current %v, pre %v, need update",
			updater.Key(), updater.Value(), preResourceUpdater.Value())
		return true
	}
	if time.Since(preResourceUpdater.GetLastUpdateTimestamp()) > time.Duration(e.Config.ResourceForceUpdateSeconds)*time.Second {
		klog.V(5).Infof("check for resource %s: last update time(%v) is earlier than (%v)s ago, need update",
			preResourceUpdater.Key(), preResourceUpdater.GetLastUpdateTimestamp(), e.Config.ResourceForceUpdateSeconds)
		return true
	}
	return false
}

func (e *ResourceUpdateExecutorImpl) update(updater ResourceUpdater) error {
	start := time.Now()
	err := updater.update()
	if err != nil && !e.isUpdateErrIgnored(err) {
		metrics.RecordResourceUpdateDuration(updater.Name(), metrics.ResourceUpdateStatusFailed, metrics.SinceInSeconds(start))
		klog.V(5).Infof("failed to update resource %s to %v, err: %v", updater.Key(), updater.Value(), err)
		return err
	} else if err != nil {
		// error can be ignored
		klog.V(5).Infof("failed to update resource %s to %v, ignored err: %v", updater.Key(), updater.Value(), err)
	} else {
		metrics.RecordResourceUpdateDuration(updater.Name(), metrics.ResourceUpdateStatusSuccess, metrics.SinceInSeconds(start))
		klog.V(6).Infof("successfully update resource %s to %v", updater.Key(), updater.Value())
	}
	return nil
}

func (e *ResourceUpdateExecutorImpl) updateByCache(updater ResourceUpdater) (bool, error) {
	if e.needUpdate(updater) {
		start := time.Now()
		err := updater.update()
		if err != nil && e.isUpdateErrIgnored(err) {
			klog.V(5).Infof("failed to cacheable update resource %s to %v, ignored err: %v", updater.Key(), updater.Value(), err)
			return false, nil
		}
		if err != nil {
			metrics.RecordResourceUpdateDuration(updater.Name(), metrics.ResourceUpdateStatusFailed, metrics.SinceInSeconds(start))
			klog.V(5).Infof("failed to cacheable update resource %s to %v, err: %v", updater.Key(), updater.Value(), err)
			return false, err
		}
		metrics.RecordResourceUpdateDuration(updater.Name(), metrics.ResourceUpdateStatusSuccess, metrics.SinceInSeconds(start))
		updater.UpdateLastUpdateTimestamp(time.Now())
		err = e.ResourceCache.SetDefault(updater.Key(), updater)
		if err != nil {
			klog.V(5).Infof("failed to SetDefault in resourceCache for resource %s, err: %v", updater.Key(), err)
			return true, err
		}
		klog.V(6).Infof("successfully cacheable update resource %s to %v", updater.Key(), updater.Value())
		return true, nil
	}
	return false, nil
}

func (e *ResourceUpdateExecutorImpl) isUpdateErrIgnored(err error) bool {
	if err == nil {
		return true
	}
	if sysutil.IsResourceUnsupportedErr(err) {
		klog.V(6).Infof("update resource failed, ignored unsupported err: %v", err)
		return true
	}
	if IsCgroupDirErr(err) {
		klog.V(6).Infof("update resource failed, ignored cgroup not exist err: %v", err)
		return true
	}
	return false
}

// NewTestResourceExecutor returns a new ResourceUpdateExecutorImpl for testing usage.
// NOTE: Please DO NOT use it except unittests.
func NewTestResourceExecutor() ResourceUpdateExecutor {
	return &ResourceUpdateExecutorImpl{
		ResourceCache: cache.NewCacheDefault(),
		Config:        NewDefaultConfig(),
	}
}
