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
	"fmt"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	apierror "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

// QuotaUpdateState provides a shared state storage for hookPlugins during quota updates
type QuotaUpdateState struct {
	Storage sync.Map
}

// QuotaInfoReader provides read-only access to quota information
type QuotaInfoReader struct {
	GetQuotaInfo       func(quotaName string) *QuotaInfo
	GetChildQuotaInfos func(quotaName string) map[string]*QuotaInfo

	// NoLock methods are used for read-only access without acquiring locks
	GetQuotaInfoNoLock       func(quotaName string) *QuotaInfo
	GetChildQuotaInfosNoLock func(quotaName string) map[string]*QuotaInfo
}

// QuotaHookPlugin defines the interface for quota management hookPlugins
type QuotaHookPlugin interface {
	// GetKey returns a unique key for the hook plugin
	GetKey() string

	// IsQuotaUpdated determines if the quota has been updated based on the hook plugin's perspective.
	// This method is invoked before updating the quota (excluding create/delete operations) with the GQM lock held.
	IsQuotaUpdated(oldQuotaInfo, newQuotaInfo *QuotaInfo, newQuota *v1alpha1.ElasticQuota) (isUpdated bool)

	// PreQuotaUpdate is invoked before performing quota create, update, or delete operations with the GQM lock held.
	// Parameters:
	// - oldQuotaInfo: The original quota information before the update. It is nil for create operations.
	// - newQuotaInfo: The updated quota information after the update. It is nil for delete operations.
	// - quota: The quota being updated. For delete operations, it is oldQuota;
	//			for create/update operations, it is newQuota.
	// - state: A shared state storage for hookPlugins during quota updates.
	PreQuotaUpdate(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState)

	// PostQuotaUpdate is invoked after performing quota create, update, or delete operations with the GQM lock held.
	// Parameters are the same as PreQuotaUpdate.
	PostQuotaUpdate(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState)

	// OnPodUpdated is triggered whenever an assigned pod's state changes within the quota,
	// including add, delete, update, reserve, or unreserve operations with the GQM lock held.
	// This method enables hook plugins to handle or modify the updated pod as needed.
	OnPodUpdated(quotaName string, oldPod, newPod *v1.Pod)

	// UpdateQuotaStatus is invoked periodically to update the quota status if needed.
	// This method allows hook plugins to modify quota status.
	// Parameters:
	// - oldQuota is the origin quota before updating status, it won't be nil.
	// - newQuota is a newly cloned quota with updated status, could be nil if no changes.
	// Return a newly cloned quota with updated status if changes are detected, otherwise nil.
	UpdateQuotaStatus(oldQuota, newQuota *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota

	// CheckPod verifies if a pod can be scheduled within the specified quota,
	// Return nil if the pod can be scheduled, otherwise return an error.
	CheckPod(quotaName string, pod *v1.Pod) error
}

type HookPluginFactory func(qiProvider *QuotaInfoReader, key, args string) (QuotaHookPlugin, error)

// hookPluginFactories is a global map of registered hook-plugin factories, keyed by factory key.
var hookPluginFactories = map[string]HookPluginFactory{}

func RegisterHookPluginFactory(factoryKey string, hookPluginFactory HookPluginFactory) {
	hookPluginFactories[factoryKey] = hookPluginFactory
}

func GetHookPluginFactory(factoryKey string) (HookPluginFactory, error) {
	hookPluginFactory := hookPluginFactories[factoryKey]
	if hookPluginFactory == nil {
		return nil, fmt.Errorf("custom limiter factory %s not found", factoryKey)
	}
	return hookPluginFactory, nil
}

func initHookPlugins(qiProvider *QuotaInfoReader, args *config.ElasticQuotaArgs) (
	plugins []QuotaHookPlugin, err error) {
	var errs []error
	for i, pluginConf := range args.HookPlugins {
		if pluginConf.Key == "" {
			errs = append(errs, fmt.Errorf("failed to initialize index-%d hook plugin: key is empty", i))
			continue
		}
		if pluginConf.FactoryKey == "" {
			errs = append(errs, fmt.Errorf("failed to initialize hook plugin %s: factory key is empty",
				pluginConf.Key))
			continue
		}
		factory, err := GetHookPluginFactory(pluginConf.FactoryKey)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to initialize hook plugin %s: factory %s not found",
				pluginConf.Key, pluginConf.FactoryKey))
			continue
		}
		plugin, err := factory(qiProvider, pluginConf.Key, pluginConf.FactoryArgs)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to initialize hook-plugin %s by factory %s, err=%v",
				pluginConf.Key, pluginConf.FactoryKey, err))
			continue
		}
		plugins = append(plugins, plugin)
	}
	if len(errs) > 0 {
		return nil, apierror.NewAggregate(errs)
	}

	klog.Infof("initialized %d hook plugins", len(plugins))
	return
}

// MetricsWrapper is a wrapper for QuotaHookPlugin that records metrics
type MetricsWrapper struct {
	plugin QuotaHookPlugin
}

// NewMetricsWrapper creates a new MetricsWrapper for a QuotaHookPlugin
func NewMetricsWrapper(plugin QuotaHookPlugin) QuotaHookPlugin {
	return &MetricsWrapper{plugin: plugin}
}

// WrapWithMetrics wraps hook plugins with metrics
func WrapWithMetrics(plugins []QuotaHookPlugin) []QuotaHookPlugin {
	if len(plugins) == 0 {
		return plugins
	}

	wrappedPlugins := make([]QuotaHookPlugin, len(plugins))
	for i, plugin := range plugins {
		wrappedPlugins[i] = NewMetricsWrapper(plugin)
	}
	return wrappedPlugins
}

func (w *MetricsWrapper) GetKey() string {
	return w.plugin.GetKey()
}

func (w *MetricsWrapper) IsQuotaUpdated(oldQuotaInfo, newQuotaInfo *QuotaInfo, newQuota *v1alpha1.ElasticQuota) bool {
	start := time.Now()
	result := w.plugin.IsQuotaUpdated(oldQuotaInfo, newQuotaInfo, newQuota)
	metrics.RecordElasticQuotaHookPluginLatency(w.GetKey(), "is_quota_updated", time.Since(start))
	return result
}

func (w *MetricsWrapper) PreQuotaUpdate(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState) {
	start := time.Now()
	w.plugin.PreQuotaUpdate(oldQuotaInfo, newQuotaInfo, quota, state)
	metrics.RecordElasticQuotaHookPluginLatency(w.GetKey(), "pre_quota_update", time.Since(start))
}

func (w *MetricsWrapper) PostQuotaUpdate(oldQuotaInfo, newQuotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota, state *QuotaUpdateState) {
	start := time.Now()
	w.plugin.PostQuotaUpdate(oldQuotaInfo, newQuotaInfo, quota, state)
	metrics.RecordElasticQuotaHookPluginLatency(w.GetKey(), "post_quota_update", time.Since(start))
}

func (w *MetricsWrapper) OnPodUpdated(quotaName string, oldPod, newPod *v1.Pod) {
	start := time.Now()
	w.plugin.OnPodUpdated(quotaName, oldPod, newPod)
	metrics.RecordElasticQuotaHookPluginLatency(w.GetKey(), "on_pod_update", time.Since(start))
}

func (w *MetricsWrapper) UpdateQuotaStatus(oldQuota, newQuota *v1alpha1.ElasticQuota) *v1alpha1.ElasticQuota {
	start := time.Now()
	result := w.plugin.UpdateQuotaStatus(oldQuota, newQuota)
	metrics.RecordElasticQuotaHookPluginLatency(w.GetKey(), "update_quota_status", time.Since(start))
	return result
}

func (w *MetricsWrapper) CheckPod(quotaName string, pod *v1.Pod) error {
	start := time.Now()
	err := w.plugin.CheckPod(quotaName, pod)
	metrics.RecordElasticQuotaHookPluginLatency(w.GetKey(), "check_pod", time.Since(start))

	return err
}

func (w *MetricsWrapper) GetPlugin() QuotaHookPlugin {
	return w.plugin
}

// The following methods in GroupQuotaManager handle interactions with hook plugins

func (gqm *GroupQuotaManager) InitHookPlugins(pluginArgs *config.ElasticQuotaArgs) error {
	hookPlugins, err := initHookPlugins(gqm.GetQuotaInfoReader(), pluginArgs)
	if err != nil {
		return fmt.Errorf("failed to init hook plugins for tree %s, err=%v", gqm.GetTreeID(), err)
	}
	gqm.SetHookPlugins(hookPlugins)
	return nil
}

func (gqm *GroupQuotaManager) GetHookPlugins() []QuotaHookPlugin {
	return gqm.hookPlugins
}

func (gqm *GroupQuotaManager) SetHookPlugins(hookPlugins []QuotaHookPlugin) {
	// Wrap plugins with metrics
	gqm.hookPlugins = WrapWithMetrics(hookPlugins)
}

func (gqm *GroupQuotaManager) GetQuotaInfoReader() *QuotaInfoReader {
	return &QuotaInfoReader{
		GetQuotaInfo:             gqm.GetQuotaInfoByName,
		GetChildQuotaInfos:       gqm.GetChildGroupQuotaInfos,
		GetQuotaInfoNoLock:       gqm.getQuotaInfoByNameNoLock,
		GetChildQuotaInfosNoLock: gqm.getChildGroupQuotaInfosNoLock,
	}
}

func (gqm *GroupQuotaManager) GetChildGroupQuotaInfos(quotaName string) map[string]*QuotaInfo {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	return gqm.getChildGroupQuotaInfosNoLock(quotaName)
}

func (gqm *GroupQuotaManager) getChildGroupQuotaInfosNoLock(quotaName string) map[string]*QuotaInfo {
	quotaTopoNode := gqm.quotaTopoNodeMap[quotaName]
	if quotaTopoNode == nil || len(quotaTopoNode.getChildGroupQuotaInfos()) == 0 {
		return nil
	}
	childQuotaInfos := make(map[string]*QuotaInfo)
	for childName := range quotaTopoNode.getChildGroupQuotaInfos() {
		if quotaInfo, ok := gqm.quotaInfoMap[childName]; ok {
			childQuotaInfos[childName] = quotaInfo
		}
	}
	return childQuotaInfos
}

// runPreQuotaUpdateHooks executes all pre-update hooks for quota changes
func (gqm *GroupQuotaManager) runPreQuotaUpdateHooks(oldQuotaInfo, newQuotaInfo *QuotaInfo,
	quota *v1alpha1.ElasticQuota) *QuotaUpdateState {
	if len(gqm.hookPlugins) == 0 {
		return nil
	}
	state := &QuotaUpdateState{}
	for _, hook := range gqm.hookPlugins {
		hook.PreQuotaUpdate(oldQuotaInfo, newQuotaInfo, quota, state)
	}
	return state
}

// runPostQuotaUpdateHooks executes all post-update hooks for quota changes
// state could be nil if quota is newly added
func (gqm *GroupQuotaManager) runPostQuotaUpdateHooks(oldQuotaInfo, newQuotaInfo *QuotaInfo,
	quota *v1alpha1.ElasticQuota, state *QuotaUpdateState) {
	if len(gqm.hookPlugins) == 0 {
		return
	}
	for _, hook := range gqm.hookPlugins {
		hook.PostQuotaUpdate(oldQuotaInfo, newQuotaInfo, quota, state)
	}
}

// runPodUpdateHooks executes all hooks after pod used resource changed
func (gqm *GroupQuotaManager) runPodUpdateHooks(quotaName string, oldPod, newPod *v1.Pod) {
	for _, hook := range gqm.hookPlugins {
		hook.OnPodUpdated(quotaName, oldPod, newPod)
	}
}

func (gqm *GroupQuotaManager) IsQuotaUpdated(oldQuotaInfo, newQuotaInfo *QuotaInfo,
	newQuota *v1alpha1.ElasticQuota) bool {
	for _, hook := range gqm.hookPlugins {
		if hook.IsQuotaUpdated(oldQuotaInfo, newQuotaInfo, newQuota) {
			return true
		}
	}
	return false
}

// ResetQuotasForHookPlugins resets quotas with pre-update and post-update hooks
func (gqm *GroupQuotaManager) ResetQuotasForHookPlugins(quotas map[string]*v1alpha1.ElasticQuota) {
	startTime := time.Now()
	defer func() {
		klog.Infof("reset hook plugins for tree %s, took %v", gqm.GetTreeID(), time.Since(startTime))
	}()

	gqm.hierarchyUpdateLock.Lock()
	defer gqm.hierarchyUpdateLock.Unlock()

	for quotaName, quotaInfo := range gqm.quotaInfoMap {
		quota := quotas[quotaInfo.Name]
		if quota == nil {
			klog.Warningf("skip resetting inconsistent quota %s for hook plugins", quotaName)
			continue
		}
		hookState := gqm.runPreQuotaUpdateHooks(nil, quotaInfo, quota)
		gqm.runPostQuotaUpdateHooks(nil, quotaInfo, quota, hookState)
	}
}
