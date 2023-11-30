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

package coresched

import (
	"fmt"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/atomic"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	name        = "CoreSched"
	description = "manage core sched cookies for pod and containers"

	ruleNameForNodeSLO = name + " (nodeSLO)"
	ruleNameForAllPods = name + " (allPods)"

	defaultCacheExpiration     = 300 * time.Second
	defaultCacheDeleteInterval = 600 * time.Second

	// DefaultExpellerSuffix is the default suffix of the expeller core sched group.
	DefaultExpellerSuffix = "-expeller"
)

// SYSTEM QoS is excluded
var podQOSConditions = []string{string(extension.QoSBE), string(extension.QoSLS), string(extension.QoSLSR),
	string(extension.QoSLSE), string(extension.QoSNone)}

// Plugin is responsible for managing core sched cookies and cpu.idle for containers.
type Plugin struct {
	rule *Rule

	initialized     *atomic.Bool // whether the cache has been initialized
	allPodsSyncOnce sync.Once    // sync once for AllPods

	sysSupported *bool
	supportedMsg string
	sysEnabled   bool

	cookieCache        *gocache.Cache // core-sched-group-id -> cookie id, set<pgid>; if the group has had cookie
	cookieCacheRWMutex sync.RWMutex
	groupCache         *gocache.Cache // pod-uid+container-id -> core-sched-group-id (note that it caches the last state); if the container has had cookie of the group

	reader   resourceexecutor.CgroupReader
	executor resourceexecutor.ResourceUpdateExecutor
	cse      sysutil.CoreSchedExtendedInterface
}

var singleton *Plugin

func Object() *Plugin {
	if singleton == nil {
		singleton = newPlugin()
	}
	return singleton
}

func newPlugin() *Plugin {
	return &Plugin{
		rule:            newRule(),
		cookieCache:     gocache.New(defaultCacheExpiration, defaultCacheDeleteInterval),
		groupCache:      gocache.New(defaultCacheExpiration, defaultCacheDeleteInterval),
		initialized:     atomic.NewBool(false),
		allPodsSyncOnce: sync.Once{},
	}
}

func (p *Plugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", name)
	// TODO: hook NRI events RunPodSandbox, PostStartContainer
	rule.Register(ruleNameForNodeSLO, description,
		rule.WithParseFunc(statesinformer.RegisterTypeNodeSLOSpec, p.parseRuleForNodeSLO),
		rule.WithUpdateCallback(p.ruleUpdateCb))
	rule.Register(ruleNameForAllPods, description,
		rule.WithParseFunc(statesinformer.RegisterTypeAllPods, p.parseForAllPods),
		rule.WithUpdateCallback(p.ruleUpdateCb))
	reconciler.RegisterCgroupReconciler(reconciler.ContainerLevel, sysutil.VirtualCoreSchedCookie,
		"set core sched cookie to process groups of container specified",
		p.SetContainerCookie, reconciler.PodQOSFilter(), podQOSConditions...)
	reconciler.RegisterCgroupReconciler(reconciler.SandboxLevel, sysutil.VirtualCoreSchedCookie,
		"set core sched cookie to process groups of sandbox container specified",
		p.SetContainerCookie, reconciler.PodQOSFilter(), podQOSConditions...)
	reconciler.RegisterCgroupReconciler(reconciler.KubeQOSLevel, sysutil.CPUIdle, "reconcile QoS level cpu idle",
		p.SetKubeQOSCPUIdle, reconciler.NoneFilter())
	p.Setup(op)
}

func (p *Plugin) Setup(op hooks.Options) {
	p.reader = op.Reader
	p.executor = op.Executor
	p.cse = sysutil.NewCoreSchedExtended()
}

func (p *Plugin) SystemSupported() (bool, string) {
	if p.sysSupported == nil {
		isSupported, msg := sysutil.IsCoreSchedSupported()
		p.sysSupported = pointer.Bool(isSupported)
		p.supportedMsg = msg
		klog.Infof("update system supported info for plugin %s, supported %v, msg %s",
			name, *p.sysSupported, p.supportedMsg)
	}
	return *p.sysSupported, p.supportedMsg
}

func (p *Plugin) InitCache(podMetas []*statesinformer.PodMeta) bool {
	if p.initialized.Load() {
		return true
	}

	synced := p.LoadAllCookies(podMetas)

	p.initialized.Store(synced)
	return synced
}

func (p *Plugin) IsCacheInited() bool {
	return p.initialized.Load()
}

func (p *Plugin) SetKubeQOSCPUIdle(proto protocol.HooksProtocol) error {
	kubeQOSCtx := proto.(*protocol.KubeQOSContext)
	if kubeQOSCtx == nil {
		return fmt.Errorf("kubeQOS protocol is nil for plugin %s", name)
	}
	kubeQOS := kubeQOSCtx.Request.KubeQOSClass

	if !p.rule.IsInited() {
		klog.V(5).Infof("plugin %s has not been inited, rule inited %v, aborted to set cpu idle for QoS %s",
			name, p.rule.IsInited(), kubeQOS)
		return nil
	}

	isCPUIdle := p.rule.IsKubeQOSCPUIdle(kubeQOS)
	if isCPUIdle {
		kubeQOSCtx.Response.Resources.CPUIdle = pointer.Int64(1)
	} else {
		kubeQOSCtx.Response.Resources.CPUIdle = pointer.Int64(0)
	}

	return nil
}

// SetContainerCookie reconciles the core sched cookie for the container.
// There are the following operations about the cookies:
//  1. Get: Get the cookie for a core sched group, firstly try to find in cache and then get from the existing PIDs.
//  2. Add: Add a new cookie for a core sched group for a container, and add a new entry into the cache, ref count = 1.
//  3. Assign: Assign a cookie of an existing core sched group for a container, and increase cookie's ref count in cache.
//     The cookies of sibling PGIDs (i.e. the PGIDs of the same core sched group) will be fetched in the Assign. If all
//     the cookies of sibling PGIDs are default or invalid, the Assign will fall back to Add.
//  4. Clear: Clear a cookie of an existing core sched group for a container (reset to default cookie 0), and the
//     cookie's reference count in the cache is decreased. The cache entry will be removed when the ref count is no
//     larger than zero.
//
// If multiple non-default cookies are assigned to existing containers of the same group, the firstly-created and
// available cookie will be retained and the PGIDs of others will be moved to the former.
// NOTE: The agent itself should be set the default cookie. It can be excluded by setting QoS to SYSTEM.
func (p *Plugin) SetContainerCookie(proto protocol.HooksProtocol) error {
	containerCtx := proto.(*protocol.ContainerContext)
	if containerCtx == nil {
		return fmt.Errorf("container protocol is nil for plugin %s", name)
	}
	if !util.IsValidContainerCgroupDir(containerCtx.Request.CgroupParent) {
		return fmt.Errorf("invalid container cgroup parent %s for plugin %s", containerCtx.Request.CgroupParent, name)
	}

	podUID := containerCtx.Request.PodMeta.UID
	// only process sandbox container or container has valid ID
	if len(podUID) <= 0 || len(containerCtx.Request.ContainerMeta.ID) <= 0 {
		return fmt.Errorf("invalid container ID for plugin %s, pod UID %s, container ID %s",
			name, podUID, containerCtx.Request.ContainerMeta.ID)
	}

	containerUID := p.getContainerUID(podUID, containerCtx.Request.ContainerMeta.ID)
	podAnnotations := containerCtx.Request.PodAnnotations
	podLabels := containerCtx.Request.PodLabels
	podMetaName := containerCtx.Request.PodMeta.String()
	containerName := containerCtx.Request.ContainerMeta.Name
	podKubeQOS := util.GetKubeQoSByCgroupParent(containerCtx.Request.CgroupParent)

	if !p.rule.IsInited() || !p.IsCacheInited() {
		klog.V(5).Infof("plugin %s has not been inited, rule inited %v, cache inited %v, aborted to set cookie for container %s/%s",
			name, p.rule.IsInited(), p.IsCacheInited(), podMetaName, containerName)
		return nil
	}

	isEnabled, groupID := p.getPodEnabledAndGroup(podAnnotations, podLabels, podKubeQOS, podUID)
	klog.V(6).Infof("manage cookie for container %s/%s, isEnabled %v, groupID %s",
		podMetaName, containerName, isEnabled, groupID)

	lastGroupID, lastCookieEntry, cookieEntry := p.getCookieCacheForContainer(groupID, containerUID)

	// expect enabled
	// 1. disabled -> enabled: Add or Assign.
	// 2. keep enabled: Check the differences of cookie, group ID and the PGIDs, and do Assign.
	if isEnabled {
		// assert groupID != "0"
		// NOTE: if the group ID changed for a enabled pod, the cookie will be updated while the old PGIDs should expire
		// in the old cookie's cache.
		pgids, err := p.getContainerPGIDs(containerCtx.Request.CgroupParent)
		if err != nil {
			klog.V(5).Infof("failed to get PGIDs for container %s/%s, err: %s", podMetaName, containerName, err)
			return nil
		}
		if len(pgids) <= 0 {
			klog.V(5).Infof("no PGID found for container %s/%s, group %s", podMetaName, containerName, groupID)
			return nil
		}

		if cookieEntry != nil {
			// firstly try Assign, if all cached sibling pgids invalid, then try Add
			// else cookie exists for group:
			// 1. assign cookie if the container has not set cookie
			// 2. assign cookie if some process of the container has missing cookie or set incoherent cookie
			targetCookieID := cookieEntry.GetCookieID()

			if notFoundPGIDs := cookieEntry.ContainsPGIDs(pgids...); len(notFoundPGIDs) <= 0 {
				klog.V(6).Infof("assign cookie for container %s/%s skipped, group %s, cookie %v, PGID num %v",
					podMetaName, containerName, groupID, cookieEntry.GetCookieID(), len(pgids))
				p.updateCookieCacheForContainer(groupID, containerUID, cookieEntry)
				recordContainerCookieMetrics(containerCtx, groupID, targetCookieID)

				return nil
			}

			// do Assign
			siblingPGIDs := cookieEntry.GetAllPGIDs()
			pgidsAssigned, sPGIDsToDelete, err := p.assignCookie(pgids, siblingPGIDs, groupID, targetCookieID)
			if err == nil {
				klog.V(4).Infof("assign cookie for container %s/%s finished, cookie %v, PGID num %v, assigned %v",
					podMetaName, containerName, targetCookieID, len(pgids), len(pgidsAssigned))

				if len(pgidsAssigned) <= 0 { // no pgid is successfully assigned
					return nil
				}

				cookieEntry.AddPGIDs(pgidsAssigned...)
				cookieEntry.DeletePGIDs(sPGIDsToDelete...)
				p.updateCookieCacheForContainer(groupID, containerUID, cookieEntry)
				recordContainerCookieMetrics(containerCtx, groupID, targetCookieID)

				return nil
			}

			klog.V(4).Infof("failed to assign cookie for container %s/%s, fallback to add new cookie, group %s, old cookie %v, PGID num %v, err: %v",
				podMetaName, containerName, groupID, targetCookieID, len(pgids), err)

			// no valid sibling PGID, fallback to Add
			cookieEntry.DeletePGIDs(sPGIDsToDelete...)
			p.cleanCookieCacheForContainer(groupID, containerUID, cookieEntry)
		}

		// group has no cookie, do Add
		cookieID, pgidAdded, err := p.addCookie(pgids, groupID)
		if err != nil {
			klog.V(4).Infof("failed to add cookie for container %s/%s, group %s, PGID num %v, err: %v",
				podMetaName, containerName, groupID, len(pgids), err)
			return nil
		}
		if cookieID <= sysutil.DefaultCoreSchedCookieID {
			klog.V(4).Infof("failed to add cookie for container %s/%s, group %s, PGID num %v, got unexpected cookie %v",
				podMetaName, containerName, groupID, len(pgids), cookieID)
			return nil
		}

		cookieEntry = newCookieCacheEntry(cookieID, pgidAdded...)
		p.updateCookieCacheForContainer(groupID, containerUID, cookieEntry)
		recordContainerCookieMetrics(containerCtx, groupID, cookieID)

		klog.V(4).Infof("add cookie for container %s/%s finished, group %s, cookie %v, PGID num %v",
			podMetaName, containerName, groupID, cookieID, len(pgids))
		return nil
	}
	// else pod disables

	// invalid lastGroupID means container not in group cache (container should be cleared or not ever added)
	// invalid lastCookieEntry means group not in cookie cache (group should be cleared)
	// let its cached PGIDs expire or removed by siblings' Assign
	if (len(lastGroupID) <= 0 || lastGroupID == slov1alpha1.CoreSchedGroupIDNone) && lastCookieEntry == nil {
		return nil
	}

	pgids, err := p.getContainerPGIDs(containerCtx.Request.CgroupParent)
	if err != nil {
		klog.V(5).Infof("failed to get PGIDs for container %s/%s, err: %s",
			podMetaName, containerName, err)
		return nil
	}
	if len(pgids) <= 0 {
		klog.V(5).Infof("no PGID found for container %s/%s, group %s", podMetaName, containerName, groupID)
		return nil
	}

	// In case the pod has group set before while no cookie entry, do Clear to fix it
	if lastCookieEntry == nil {
		lastCookieEntry = newCookieCacheEntry(sysutil.DefaultCoreSchedCookieID)
	}
	lastCookieID := lastCookieEntry.GetCookieID()

	// do Clear:
	// - clear cookie if any process of the container has set cookie
	pgidsToClear := p.clearCookie(pgids, lastGroupID, lastCookieID)
	lastCookieEntry.DeletePGIDs(pgidsToClear...)
	p.cleanCookieCacheForContainer(lastGroupID, containerUID, lastCookieEntry)
	resetContainerCookieMetrics(containerCtx, lastGroupID, lastCookieID)

	klog.V(4).Infof("clear cookie for container %s/%s finished, last group %s, last cookie %v, PGID num %v",
		podMetaName, containerName, lastGroupID, lastCookieID, len(pgids))

	return nil
}

func (p *Plugin) LoadAllCookies(podMetas []*statesinformer.PodMeta) bool {
	hasSynced := false
	p.cookieCacheRWMutex.Lock()
	defer p.cookieCacheRWMutex.Unlock()
	for _, podMeta := range podMetas {
		pod := podMeta.Pod
		podAnnotations := pod.Annotations
		podLabels := pod.Labels
		podUID := string(pod.UID)

		if !podMeta.IsRunningOrPending() {
			klog.V(6).Infof("skip sync core sched cookie for pod %s, pod is non-running, phase %s",
				podMeta.Key(), pod.Status.Phase)
			continue
		}

		isEnabled, groupID := p.getPodEnabledAndGroup(podAnnotations, podLabels, extension.GetKubeQosClass(pod), podUID)

		containerPGIDs := p.getAllContainerPGIDs(podMeta)

		for containerID, cPGID := range containerPGIDs {
			pgids := cPGID.PGID
			if len(pgids) <= 0 {
				klog.V(5).Infof("aborted to get PGIDs for container %s/%s, err: no available PGID",
					podMeta.Key(), containerID)
				continue
			}

			cookieID, pgidsSynced, err := p.getCookie(pgids, groupID)
			if err != nil {
				klog.V(4).Infof("failed to sync cookie for container %s/%s, err: %s",
					podMeta.Key(), containerID, err)
				continue
			}

			// container synced including using the default cookie
			hasSynced = true

			if cookieID <= sysutil.DefaultCoreSchedCookieID {
				klog.V(6).Infof("skipped to sync cookie for container %s/%s, default cookie is set, enabled %v, group %s",
					podMeta.Key(), containerID, isEnabled, groupID)
				continue
			}

			var cookieEntry *CookieCacheEntry
			cookieEntryIf, groupHasCookie := p.cookieCache.Get(groupID)
			if groupHasCookie {
				cookieEntry = cookieEntryIf.(*CookieCacheEntry)
				// If multiple cookie exists for a group, aborted to sync cache. Let the reconciliation fix these.
				if lastCookieID := cookieEntry.GetCookieID(); lastCookieID > sysutil.DefaultCoreSchedCookieID &&
					lastCookieID != cookieID {
					klog.Warningf("sync cookie for container %s/%s failed, isEnabled %v, groupID %s, cookie %v, but got existing cookie %v",
						podMeta.Key(), containerID, isEnabled, groupID, cookieID, lastCookieID)
					continue
				}
				cookieEntry.AddPGIDs(pgidsSynced...)
			} else {
				cookieEntry = newCookieCacheEntry(cookieID, pgidsSynced...)
			}

			p.cookieCache.SetDefault(groupID, cookieEntry)
			containerUID := p.getContainerUID(podUID, containerID)
			p.groupCache.SetDefault(containerUID, groupID)
			klog.V(4).Infof("sync cookie for container %s/%s finished, isEnabled %v, groupID %s, cookie %v",
				podMeta.Key(), containerID, isEnabled, groupID, cookieID)
			metrics.RecordContainerCoreSchedCookie(pod.Namespace, pod.Name, podUID, cPGID.ContainerName, containerID,
				groupID, cookieID)
		}
	}

	return hasSynced
}

// getCookieCacheForPod gets the last group ID, the last cookie entry and the cookie entry for the current group.
func (p *Plugin) getCookieCacheForContainer(groupID, containerUID string) (string, *CookieCacheEntry, *CookieCacheEntry) {
	p.cookieCacheRWMutex.RLock()
	defer p.cookieCacheRWMutex.RUnlock()

	lastGroupIDIf, containerHasGroup := p.groupCache.Get(containerUID)
	lastGroupID := slov1alpha1.CoreSchedGroupIDNone
	if containerHasGroup {
		lastGroupID = lastGroupIDIf.(string)
	}

	lastCookieEntryIf, lastGroupHasCookie := p.cookieCache.Get(lastGroupID)
	var lastCookieEntry *CookieCacheEntry
	if lastGroupHasCookie {
		lastCookieEntry = lastCookieEntryIf.(*CookieCacheEntry)
		if lastCookieEntry.IsEntryInvalid() { // no valid cookie ref
			lastCookieEntry = nil
		}
	}

	cookieEntryIf, groupHasCookie := p.cookieCache.Get(groupID)
	var cookieEntry *CookieCacheEntry
	if groupHasCookie {
		cookieEntry = cookieEntryIf.(*CookieCacheEntry)
		if cookieEntry.IsEntryInvalid() { // no valid cookie ref
			cookieEntry = nil
		}
	}

	return lastGroupID, lastCookieEntry, cookieEntry
}

func (p *Plugin) updateCookieCacheForContainer(groupID, containerUID string, cookieEntry *CookieCacheEntry) {
	p.cookieCacheRWMutex.Lock()
	defer p.cookieCacheRWMutex.Unlock()
	p.groupCache.SetDefault(containerUID, groupID)
	if cookieEntry.IsEntryInvalid() {
		p.cookieCache.Delete(groupID)
	} else {
		p.cookieCache.SetDefault(groupID, cookieEntry)
	}
}

func (p *Plugin) cleanCookieCacheForContainer(groupID, containerUID string, cookieEntry *CookieCacheEntry) {
	p.cookieCacheRWMutex.Lock()
	defer p.cookieCacheRWMutex.Unlock()
	p.groupCache.Delete(containerUID)
	if cookieEntry.IsEntryInvalid() {
		p.cookieCache.Delete(groupID)
	} else {
		p.cookieCache.SetDefault(groupID, cookieEntry)
	}
}
