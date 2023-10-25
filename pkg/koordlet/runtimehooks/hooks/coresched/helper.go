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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

type containerPGID struct {
	ContainerName string
	PGID          []uint32
}

// getCookie retrieves the last core sched cookies applied to the PGIDs.
// If multiple cookies are set for the PGIDs, only the first non-default cookie is picked.
// It returns the last cookie ID, PGIDs synced and the error.
func (p *Plugin) getCookie(pgids []uint32, groupID string) (uint64, []uint32, error) {
	if len(pgids) <= 0 {
		klog.V(6).Infof("aborted to sync PGIDs cookie for group %s, no PGID", groupID)
		return 0, nil, nil
	}
	newCookieIDMap := map[uint64]struct{}{}
	firstNewCookieID := sysutil.DefaultCoreSchedCookieID
	var newCookiePGIDs []uint32
	for _, pgid := range pgids {
		cookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pgid)
		if err != nil {
			klog.V(6).Infof("failed to sync last cookie for PGID %v, group %s, err: %s", pgid, groupID, err)
			continue
		}
		if cookieID != sysutil.DefaultCoreSchedCookieID {
			newCookieIDMap[cookieID] = struct{}{}

			if firstNewCookieID == sysutil.DefaultCoreSchedCookieID {
				firstNewCookieID = cookieID
			}
			if cookieID == firstNewCookieID {
				newCookiePGIDs = append(newCookiePGIDs, pgid)
			}
		}
	}

	if len(newCookieIDMap) <= 0 { // no cookie to sync, all PGIDs are default or unknown
		return 0, nil, nil
	}

	if len(newCookieIDMap) == 1 { // only one cookie to sync
		return firstNewCookieID, newCookiePGIDs, nil
	}
	// else newCookieIDMap > 1

	// When got more than one non-default cookie for given group, use the first synced new cookie ID.
	// Let the PGIDs of different cookies fixed by the next container-level reconciliation.
	klog.V(4).Infof("unexpected number of cookies to sync, group %s, cookie ID %v, found cookie num %v, PGID num %v",
		groupID, firstNewCookieID, len(newCookieIDMap), len(pgids))
	return firstNewCookieID, newCookiePGIDs, nil
}

// addCookie creates a new cookie for the given PGIDs[0], and assign the cookie to PGIDs[1:].
// It returns the new cookie ID, the assigned PGIDs, and the error.
// TODO: refactor to resource updater.
func (p *Plugin) addCookie(pgids []uint32, groupID string) (uint64, []uint32, error) {
	if len(pgids) <= 0 {
		klog.V(6).Infof("aborted to add PGIDs cookie for group %s, no PGID", groupID)
		return 0, nil, nil
	}
	lastCookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pgids[0])
	if err != nil {
		return 0, nil, fmt.Errorf("get last cookie ID for PGID %v failed, err: %s", pgids[0], err)
	}
	if lastCookieID != sysutil.DefaultCoreSchedCookieID { // perhaps the group is changed
		klog.V(5).Infof("last cookie ID for PGID %v is not default, group %s, cookie expect %v but got %v",
			pgids[0], groupID, sysutil.DefaultCoreSchedCookieID, lastCookieID)
	}

	err = p.cse.Create(sysutil.CoreSchedScopeProcessGroup, pgids[0])
	if err != nil {
		return 0, nil, fmt.Errorf("create cookie for PGID %v failed, err: %s", pgids[0], err)
	}
	cookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pgids[0])
	if err != nil {
		return 0, nil, fmt.Errorf("get new cookie ID for PGID %v failed, err: %s", pgids[0], err)
	}

	failedPIDs, err := p.cse.Assign(sysutil.CoreSchedScopeThread, pgids[0], sysutil.CoreSchedScopeProcessGroup, pgids[1:]...)
	if err != nil {
		klog.V(5).Infof("failed to assign new cookie for group %s, cookie %v, PGID from %v, PGID to %v failed of %v, err: %s",
			groupID, cookieID, pgids[0], len(failedPIDs), len(pgids)-1, err)
	}

	pgidsAdded := newUint32OrderedMap(pgids...)
	pgidsAdded.DeleteAny(failedPIDs...)

	return cookieID, pgidsAdded.GetAll(), nil
}

// assignCookie assigns the target cookieID to the given PGIDs.
// It returns the PGIDs assigned, PGIDs to delete, and the error (when exists, fallback adding new cookie).
// TODO: refactor to resource updater.
func (p *Plugin) assignCookie(pgids, siblingPGIDs []uint32, groupID string, targetCookieID uint64) ([]uint32, []uint32, error) {
	if len(pgids) <= 0 {
		klog.V(6).Infof("aborted to assign PGIDs cookie for group %s, target cookie %v, no PGID",
			targetCookieID, groupID)
		return nil, nil, nil
	}
	pgidsToAssign := newUint32OrderedMap()
	var pgidsAssigned []uint32
	unknownCount := 0
	for _, pgid := range pgids {
		lastCookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pgid)
		if err != nil {
			klog.V(6).Infof("failed to get cookie for PGID %v during assign, group %s, err: %s",
				pgid, groupID, err)
			unknownCount++
			continue
		}
		if lastCookieID != targetCookieID {
			pgidsToAssign.Add(pgid)
		} else {
			pgidsAssigned = append(pgidsAssigned, pgid)
		}
	}

	if unknownCount >= len(pgids) { // in case the given pgids terminate, e.g. the container is restarting, aborted
		klog.V(5).Infof("failed to get last cookie for group %s, got %v unknown of %v PGIDs",
			groupID, unknownCount, len(pgids))
		return nil, nil, nil
	}

	if pgidsToAssign.Len() <= 0 { // all PGIDs are assigned, just refresh reference
		return pgidsAssigned, nil, nil
	}

	var sPGIDsToDelete []uint32
	validSiblingPGID := uint32(0) // find one valid sibling PGID to share from
	for _, sPGID := range siblingPGIDs {
		pCookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, sPGID)
		if err != nil {
			klog.V(6).Infof("failed to get cookie for sibling PGID %v, group %s, err: %s",
				sPGID, groupID, err)
			sPGIDsToDelete = append(sPGIDsToDelete, sPGID)
			continue
		}
		if pCookieID != targetCookieID {
			klog.V(6).Infof("failed to get target cookie for sibling PGID %v, err: expect %v but got %v",
				sPGID, targetCookieID, pCookieID)
			sPGIDsToDelete = append(sPGIDsToDelete, sPGID)
			continue
		}

		// get the first valid sibling PGID
		validSiblingPGID = sPGID
		break
	}

	if validSiblingPGID == 0 {
		return nil, sPGIDsToDelete, fmt.Errorf("no valid sibling PGID, sibling PGIDs to delete num %v",
			len(sPGIDsToDelete))
	}

	// assign to valid sibling PGID
	failedPGIDs, err := p.cse.Assign(sysutil.CoreSchedScopeThread, validSiblingPGID, sysutil.CoreSchedScopeProcessGroup, pgidsToAssign.GetAll()...)
	if err != nil {
		klog.V(5).Infof("failed to assign group cookie for group %s, target cookie %v, PGID from %v, PGID to %v failed of %v, err: %s",
			groupID, targetCookieID, validSiblingPGID, len(failedPGIDs), pgidsToAssign.Len(), err)
		pgidsToAssign.DeleteAny(failedPGIDs...)
	}
	pgidsToAssign.AddAny(pgidsAssigned...)

	return pgidsToAssign.GetAll(), sPGIDsToDelete, nil
}

// clearCookie clears the cookie for the given PGIDs to the default cookie 0.
// It returns the PGIDs cleared.
func (p *Plugin) clearCookie(pgids []uint32, groupID string, lastCookieID uint64) []uint32 {
	if len(pgids) <= 0 {
		klog.V(6).Infof("aborted to clear PGIDs cookie for group %s, no PGID", groupID)
		return nil
	}
	pgidsToClear := newUint32OrderedMap()
	var pgidsCleared []uint32
	for _, pgid := range pgids {
		pCookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pgid)
		if err != nil {
			klog.V(6).Infof("failed to get cookie for PGID %v, group %s, err: %s", pgid, groupID, err)
			continue
		}
		if pCookieID != sysutil.DefaultCoreSchedCookieID {
			pgidsToClear.Add(pgid)
		} else {
			pgidsCleared = append(pgidsCleared, pgid)
		}
	}

	if pgidsToClear.Len() <= 0 {
		return pgidsCleared
	}

	failedPIDs, err := p.cse.Clear(sysutil.CoreSchedScopeProcessGroup, pgidsToClear.GetAll()...)
	if err != nil {
		klog.V(4).Infof("failed to clear cookie for group, last cookie %v, PGID %v failed of %v, total %v, err: %s",
			groupID, lastCookieID, len(failedPIDs), pgidsToClear.GetAll(), len(pgids), err)
		pgidsToClear.DeleteAny(failedPIDs...)
	}
	pgidsToClear.AddAny(pgidsCleared...)

	return pgidsToClear.GetAll()
}

// getPodEnabledAndGroup gets whether the pod enables the core scheduling and the group ID if it does.
func (p *Plugin) getPodEnabledAndGroup(podAnnotations, podLabels map[string]string, podKubeQOS corev1.PodQOSClass, podUID string) (bool, string) {
	// if the pod enables/disables the core-sched explicitly
	groupID, isPodDisabled := slov1alpha1.GetCoreSchedGroupID(podAnnotations)
	if isPodDisabled != nil && *isPodDisabled { // pod disables
		return false, groupID
	}

	podQOS := extension.QoSNone
	if podLabels != nil {
		podQOS = extension.GetQoSClassByAttrs(podLabels, podAnnotations)
	}
	isQOSEnabled, isExpeller := p.rule.IsPodEnabled(podQOS, podKubeQOS)
	groupID = p.getGroupID(groupID, podUID, isExpeller)

	if isPodDisabled != nil { // assert *isPodDisabled == true
		return true, groupID
	}

	// use the QoS-level rules
	return isQOSEnabled, groupID
}

func (p *Plugin) getGroupID(baseGroupID string, podUID string, isExpeller bool) string {
	var groupID string
	if len(baseGroupID) > 0 {
		groupID = baseGroupID
	} else {
		groupID = podUID
	}
	if isExpeller {
		groupID += DefaultExpellerSuffix
	}
	return groupID
}

func (p *Plugin) getContainerUID(podUID string, containerID string) string {
	return podUID + "/" + containerID
}

func (p *Plugin) getContainerPGIDs(containerCgroupParent string) ([]uint32, error) {
	containerPIDs, err := p.reader.ReadCPUProcs(containerCgroupParent)
	if err != nil && resourceexecutor.IsCgroupDirErr(err) {
		klog.V(5).Infof("aborted to get PGIDs for container dir %s, err: %s",
			containerCgroupParent, err)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get container PIDs failed, err: %w", err)
	}
	pgids, err := sysutil.GetPGIDsForPIDs(containerPIDs)
	if err != nil {
		return nil, fmt.Errorf("get container PGID failed, PID num %v, err: %w", len(containerPIDs), err)
	}
	return pgids, nil
}

func (p *Plugin) getSandboxContainerPGIDs(podMeta *statesinformer.PodMeta) ([]uint32, string, error) {
	sandboxID, err := util.GetPodSandboxContainerID(podMeta.Pod)
	if err != nil {
		return nil, "", fmt.Errorf("get sandbox container ID failed, err: %w", err)
	}
	sandboxContainerDir, err := util.GetContainerCgroupParentDirByID(podMeta.CgroupDir, sandboxID)
	if err != nil {
		return nil, sandboxID, fmt.Errorf("get cgroup parent for sandbox container %s/%s, err: %w",
			podMeta.Key(), sandboxID, err)
	}
	pgids, err := p.getContainerPGIDs(sandboxContainerDir)
	if err != nil {
		return nil, sandboxID, fmt.Errorf("get PGID failed for sandbox container %s/%s, parent dir %s, err: %w",
			podMeta.Key(), sandboxID, sandboxContainerDir, err)
	}
	return pgids, sandboxID, nil
}

func (p *Plugin) getNormalContainerPGIDs(podMeta *statesinformer.PodMeta, containerStatus *corev1.ContainerStatus) ([]uint32, error) {
	var pgids []uint32
	containerDir, err := util.GetContainerCgroupParentDir(podMeta.CgroupDir, containerStatus)
	if err != nil {
		return nil, fmt.Errorf("get cgroup parent for container %s/%s, err: %w",
			podMeta.Key(), containerStatus.Name, err)
	}
	pgids, err = p.getContainerPGIDs(containerDir)
	if err != nil {
		return nil, fmt.Errorf("get PGID failed for container %s/%s, parent dir %s, err: %w",
			podMeta.Key(), containerStatus.Name, containerDir, err)
	}
	return pgids, nil
}

func (p *Plugin) getAllContainerPGIDs(podMeta *statesinformer.PodMeta) map[string]*containerPGID {
	containerToPGIDs := map[string]*containerPGID{}
	count := 0
	pod := podMeta.Pod

	// for sandbox container
	sandboxPGIDs, sandboxContainerID, err := p.getSandboxContainerPGIDs(podMeta)
	if err != nil {
		klog.V(5).Infof("failed to get sandbox container PGID for pod %s, err: %s", podMeta.Key(), err)
	} else {
		containerToPGIDs[sandboxContainerID] = &containerPGID{
			PGID: sandboxPGIDs,
		}
		count += len(sandboxPGIDs)
	}

	// for containers
	containerMap := make(map[string]*corev1.Container, len(pod.Spec.Containers))
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		containerMap[container.Name] = container
	}
	for i := range pod.Status.ContainerStatuses {
		containerStat := &pod.Status.ContainerStatuses[i]
		if containerStat.State.Running == nil || len(containerStat.ContainerID) <= 0 {
			klog.V(6).Infof("skip sync core sched cookie for non-running container %s/%s, ID %s, state %+v",
				podMeta.Key(), containerStat.Name, containerStat.ContainerID, containerStat.State)
			continue
		}

		container, exist := containerMap[containerStat.Name]
		if !exist {
			klog.V(5).Infof("failed to find container %s/%s during sync core sched cookie",
				podMeta.Key(), containerStat.Name)
			continue
		}

		containerPGIDs, err := p.getNormalContainerPGIDs(podMeta, containerStat)
		if err != nil {
			klog.V(5).Infof("failed to get container %s PGID for pod %s, err: %s",
				container.Name, podMeta.Key(), err)
			continue
		}

		containerToPGIDs[containerStat.ContainerID] = &containerPGID{
			ContainerName: containerStat.Name,
			PGID:          containerPGIDs,
		}
		count += len(containerPGIDs)
	}

	klog.V(6).Infof("get PGIDs for pod %s finished, PGID num %v", podMeta.Key(), count)
	return containerToPGIDs
}

func recordContainerCookieMetrics(containerCtx *protocol.ContainerContext, groupID string, cookieID uint64) {
	metrics.RecordContainerCoreSchedCookie(containerCtx.Request.PodMeta.Namespace,
		containerCtx.Request.PodMeta.Name, containerCtx.Request.PodMeta.UID,
		containerCtx.Request.ContainerMeta.Name, containerCtx.Request.ContainerMeta.ID,
		groupID, cookieID)
}

func resetContainerCookieMetrics(containerCtx *protocol.ContainerContext, groupID string, lastCookieID uint64) {
	metrics.ResetContainerCoreSchedCookie(containerCtx.Request.PodMeta.Namespace,
		containerCtx.Request.PodMeta.Name, containerCtx.Request.PodMeta.UID,
		containerCtx.Request.ContainerMeta.Name, containerCtx.Request.ContainerMeta.ID,
		groupID, lastCookieID)
}
