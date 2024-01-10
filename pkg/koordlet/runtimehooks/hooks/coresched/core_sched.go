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

	// ExpellerGroupSuffix is the default suffix of the expeller core sched group.
	ExpellerGroupSuffix = "-expeller"
)

// SYSTEM QoS is excluded from the cookie mutating.
// All SYSTEM pods use the default cookie so the agent can reset the cookie of a container by ShareTo its cookie to
// the target.
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

	cookieCache        *gocache.Cache // core-sched-group-id -> cookie id, set<pid>; if the group has had cookie
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
	// TODO: support host application
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
		isSupported, msg := sysutil.EnableCoreSchedIfSupported()
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
//  1. Get: Get the cookie for a core sched group, firstly try finding in cache and then get from the existing PIDs.
//  2. Add: Add a new cookie for a core sched group for a container, and add a new entry into the cache.
//  3. Assign: Assign a cookie of an existing core sched group for a container and update the cache entry.
//     The cached sibling PIDs (i.e. the PIDs of the same core sched group) will be fetched in the Assign. If all
//     cookies of the sibling PIDs are default or invalid, the Assign should fall back to Add.
//  4. Clear: Clear a cookie of an existing core sched group for a container (reset to default cookie 0), and the
//     containers' PIDs are removed from the cache. The cache entry of the group is removed when the number of the
//     cached PIDs decreases to zero.
//
// If multiple non-default cookies are assigned to existing containers of the same group, the firstly-created and
// available cookie will be retained and the PIDs of others will be moved to the former.
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

	if !p.rule.IsInited() || !p.IsCacheInited() {
		klog.V(5).Infof("plugin %s has not been inited, rule inited %v, cache inited %v, aborted to set cookie for container %s/%s",
			name, p.rule.IsInited(), p.IsCacheInited(), containerCtx.Request.PodMeta.String(), containerCtx.Request.ContainerMeta.Name)
		return nil
	}

	isEnabled, groupID := p.getPodEnabledAndGroup(containerCtx.Request.PodAnnotations, containerCtx.Request.PodLabels,
		util.GetKubeQoSByCgroupParent(containerCtx.Request.CgroupParent), podUID)
	klog.V(6).Infof("manage cookie for container %s/%s, isEnabled %v, groupID %s",
		containerCtx.Request.PodMeta.String(), containerCtx.Request.ContainerMeta.Name, isEnabled, groupID)

	// expect enabled
	// 1. disabled -> enabled: Add or Assign.
	// 2. keep enabled: Check the differences of cookie, group ID and the PIDs, and do Assign.
	if isEnabled {
		return p.enableContainerCookie(containerCtx, groupID)
	}
	// else pod disables

	return p.disableContainerCookie(containerCtx, groupID)
}

// LoadAllCookies syncs the current core sched cookies of all pods into the cookie cache.
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

		containerPIDs := p.getAllContainerPIDs(podMeta)

		for _, cPID := range containerPIDs {
			containerID := cPID.ContainerID
			pids := cPID.PID
			if len(pids) <= 0 {
				klog.V(5).Infof("aborted to get PIDs for container %s/%s, err: no available PID",
					podMeta.Key(), containerID)
				continue
			}

			cookieID, pidsSynced, err := p.getCookie(pids, groupID)
			if err != nil {
				klog.V(0).Infof("failed to sync cookie for container %s/%s, err: %s",
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
				cookieEntry.AddPIDs(pidsSynced...)
			} else {
				cookieEntry = newCookieCacheEntry(cookieID, pidsSynced...)
			}

			p.cookieCache.SetDefault(groupID, cookieEntry)
			containerUID := p.getContainerUID(podUID, containerID)
			p.groupCache.SetDefault(containerUID, groupID)
			klog.V(4).Infof("sync cookie for container %s/%s finished, isEnabled %v, groupID %s, cookie %v",
				podMeta.Key(), containerID, isEnabled, groupID, cookieID)
			metrics.RecordContainerCoreSchedCookie(pod.Namespace, pod.Name, podUID, cPID.ContainerName, containerID,
				groupID, cookieID)
		}
	}

	return hasSynced
}

// enableContainerCookie adds or assigns a core sched cookie for the container.
func (p *Plugin) enableContainerCookie(containerCtx *protocol.ContainerContext, groupID string) error {
	podMetaName := containerCtx.Request.PodMeta.String()
	containerName := containerCtx.Request.ContainerMeta.Name
	podUID := containerCtx.Request.PodMeta.UID
	containerUID := p.getContainerUID(podUID, containerCtx.Request.ContainerMeta.ID)
	lastGroupID, _, cookieEntry := p.getCookieCacheForContainer(groupID, containerUID)

	// assert groupID != "0"
	// NOTE: if the group ID changed for a enabled pod, the cookie will be updated while the old PIDs should expire
	// in the old cookie's cache.
	pids, err := p.getContainerPIDs(containerCtx.Request.CgroupParent)
	if err != nil {
		klog.V(5).Infof("failed to get PIDs for container %s/%s, err: %s", podMetaName, containerName, err)
		return nil
	}
	if len(pids) <= 0 {
		klog.V(5).Infof("no PID found for container %s/%s, group %s", podMetaName, containerName, groupID)
		return nil
	}

	if cookieEntry != nil {
		// firstly try Assign, if all cached sibling pids invalid, then try Add
		// else cookie exists for group:
		// 1. assign cookie if the container has not set cookie
		// 2. assign cookie if some process of the container has missing cookie or set incoherent cookie
		targetCookieID := cookieEntry.GetCookieID()

		if notFoundPIDs := cookieEntry.ContainsPIDs(pids...); len(notFoundPIDs) <= 0 {
			klog.V(6).Infof("assign cookie for container %s/%s skipped, group %s, cookie %v, PID num %v",
				podMetaName, containerName, groupID, cookieEntry.GetCookieID(), len(pids))
			p.updateCookieCacheForContainer(groupID, containerUID, cookieEntry)
			recordContainerCookieMetrics(containerCtx, groupID, targetCookieID)

			return nil
		}

		// do Assign
		siblingPIDs := cookieEntry.GetAllPIDs()
		pidsAssigned, sPIDsToDelete, err := p.assignCookie(pids, siblingPIDs, groupID, targetCookieID)
		if err == nil {
			if lastGroupID != groupID {
				klog.V(4).Infof("assign cookie for container %s/%s finished, last group %s, group %s, cookie %v, PID num %v, assigned %v",
					podMetaName, containerName, lastGroupID, groupID, targetCookieID, len(pids), len(pidsAssigned))
			} else {
				klog.V(5).Infof("assign cookie for container %s/%s finished, cookie %v, PID num %v, assigned %v",
					podMetaName, containerName, targetCookieID, len(pids), len(pidsAssigned))
			}

			if len(pidsAssigned) <= 0 { // no pid is successfully assigned
				return nil
			}

			cookieEntry.AddPIDs(pidsAssigned...)
			cookieEntry.DeletePIDs(sPIDsToDelete...)
			p.updateCookieCacheForContainer(groupID, containerUID, cookieEntry)
			recordContainerCookieMetrics(containerCtx, groupID, targetCookieID)

			return nil
		}

		metrics.RecordCoreSchedCookieManageStatus(groupID, false)
		klog.V(4).Infof("failed to assign cookie for container %s/%s, fallback to add new cookie, group %s, old cookie %v, PID num %v, err: %v",
			podMetaName, containerName, groupID, targetCookieID, len(pids), err)

		// no valid sibling PID, fallback to Add
		cookieEntry.DeletePIDs(sPIDsToDelete...)
		p.cleanCookieCacheForContainer(groupID, containerUID, cookieEntry)
	}

	// group has no cookie, do Add
	cookieID, pidAdded, err := p.addCookie(pids, groupID)
	if err != nil {
		metrics.RecordCoreSchedCookieManageStatus(groupID, false)
		klog.V(4).Infof("failed to add cookie for container %s/%s, group %s, PID num %v, err: %v",
			podMetaName, containerName, groupID, len(pids), err)
		return nil
	}
	if cookieID <= sysutil.DefaultCoreSchedCookieID {
		klog.V(4).Infof("failed to add cookie for container %s/%s, group %s, PID num %v, got unexpected cookie %v",
			podMetaName, containerName, groupID, len(pids), cookieID)
		return nil
	}

	cookieEntry = newCookieCacheEntry(cookieID, pidAdded...)
	p.updateCookieCacheForContainer(groupID, containerUID, cookieEntry)
	recordContainerCookieMetrics(containerCtx, groupID, cookieID)

	klog.V(4).Infof("add cookie for container %s/%s finished, group %s, cookie %v, PID num %v",
		podMetaName, containerName, groupID, cookieID, len(pids))
	return nil
}

// disableContainerCookie clears a core sched cookie for the container.
func (p *Plugin) disableContainerCookie(containerCtx *protocol.ContainerContext, groupID string) error {
	podMetaName := containerCtx.Request.PodMeta.String()
	containerName := containerCtx.Request.ContainerMeta.Name
	podUID := containerCtx.Request.PodMeta.UID
	containerUID := p.getContainerUID(podUID, containerCtx.Request.ContainerMeta.ID)
	lastGroupID, lastCookieEntry, _ := p.getCookieCacheForContainer(groupID, containerUID)

	// invalid lastGroupID means container not in group cache (container should be cleared or not ever added)
	// invalid lastCookieEntry means group not in cookie cache (group should be cleared)
	// let its cached PIDs expire or removed by siblings' Assign
	if (len(lastGroupID) <= 0 || lastGroupID == slov1alpha1.CoreSchedGroupIDNone) && lastCookieEntry == nil {
		return nil
	}

	pids, err := p.getContainerPIDs(containerCtx.Request.CgroupParent)
	if err != nil {
		klog.V(5).Infof("failed to get PIDs for container %s/%s, err: %s",
			podMetaName, containerName, err)
		return nil
	}
	if len(pids) <= 0 {
		klog.V(5).Infof("no PID found for container %s/%s, group %s", podMetaName, containerName, groupID)
		return nil
	}

	// In case the pod has group set before while no cookie entry, do Clear to fix it
	if lastCookieEntry == nil {
		lastCookieEntry = newCookieCacheEntry(sysutil.DefaultCoreSchedCookieID)
	}
	lastCookieID := lastCookieEntry.GetCookieID()

	// do Clear:
	// - clear cookie if any process of the container has set cookie
	pidsToClear := p.clearCookie(pids, lastGroupID, lastCookieID)
	lastCookieEntry.DeletePIDs(pidsToClear...)
	p.cleanCookieCacheForContainer(lastGroupID, containerUID, lastCookieEntry)
	resetContainerCookieMetrics(containerCtx, lastGroupID, lastCookieID)

	klog.V(4).Infof("clear cookie for container %s/%s finished, last group %s, last cookie %v, PID num %v",
		podMetaName, containerName, lastGroupID, lastCookieID, len(pids))

	return nil
}

// getCookieCacheForPod gets the last group ID, the last cookie entry and the cookie entry for the current group.
// If a pod has not set cookie before, return lastGroupID=0 and lastCookieEntry=nil.
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
