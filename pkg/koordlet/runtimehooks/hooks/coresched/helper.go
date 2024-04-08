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

type containerPID struct {
	ContainerName string
	ContainerID   string
	PID           []uint32
}

// getCookie retrieves the last core sched cookies applied to the PIDs.
// If multiple cookies are set for the PIDs, only the first non-default cookie is picked.
// It returns the last cookie ID, PIDs synced and the error.
func (p *Plugin) getCookie(pids []uint32, groupID string) (uint64, []uint32, error) {
	if len(pids) <= 0 {
		klog.V(6).Infof("aborted to sync PIDs cookie for group %s, no PID", groupID)
		return 0, nil, nil
	}
	newCookieIDMap := map[uint64]struct{}{}
	firstNewCookieID := sysutil.DefaultCoreSchedCookieID
	var newCookiePIDs []uint32
	for _, pid := range pids {
		cookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pid)
		if err != nil {
			klog.V(6).Infof("failed to sync last cookie for PID %v, group %s, err: %s", pid, groupID, err)
			continue
		}
		if cookieID != sysutil.DefaultCoreSchedCookieID {
			newCookieIDMap[cookieID] = struct{}{}

			if firstNewCookieID == sysutil.DefaultCoreSchedCookieID {
				firstNewCookieID = cookieID
			}
			if cookieID == firstNewCookieID {
				newCookiePIDs = append(newCookiePIDs, pid)
			}
		}
	}

	if len(newCookieIDMap) <= 0 { // no cookie to sync, all PIDs are default or unknown
		return 0, nil, nil
	}

	if len(newCookieIDMap) == 1 { // only one cookie to sync
		return firstNewCookieID, newCookiePIDs, nil
	}
	// else newCookieIDMap > 1

	// When got more than one non-default cookie for given group, use the first synced new cookie ID.
	// Let the PIDs of different cookies fixed by the next container-level reconciliation.
	klog.V(4).Infof("unexpected number of cookies to sync, group %s, cookie ID %v, found cookie num %v, PID num %v",
		groupID, firstNewCookieID, len(newCookieIDMap), len(pids))
	return firstNewCookieID, newCookiePIDs, nil
}

// addCookie creates a new cookie for the given PIDs[0], and assign the cookie to PIDs[1:].
// It returns the new cookie ID, the assigned PIDs, and the error.
// TODO: refactor to resource updater.
func (p *Plugin) addCookie(pids []uint32, groupID string) (uint64, []uint32, error) {
	if len(pids) <= 0 {
		klog.V(6).Infof("aborted to add PIDs cookie for group %s, no PID", groupID)
		return 0, nil, nil
	}
	lastCookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pids[0])
	if err != nil {
		return 0, nil, fmt.Errorf("get last cookie ID for PID %v failed, err: %s", pids[0], err)
	}
	if lastCookieID != sysutil.DefaultCoreSchedCookieID { // perhaps the group is changed
		klog.V(5).Infof("last cookie ID for PID %v is not default, group %s, cookie expect %v but got %v",
			pids[0], groupID, sysutil.DefaultCoreSchedCookieID, lastCookieID)
	}

	err = p.cse.Create(sysutil.CoreSchedScopeThreadGroup, pids[0])
	if err != nil {
		return 0, nil, fmt.Errorf("create cookie for PID %v failed, err: %s", pids[0], err)
	}
	cookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pids[0])
	if err != nil {
		return 0, nil, fmt.Errorf("get new cookie ID for PID %v failed, err: %s", pids[0], err)
	}

	failedPIDs, err := p.cse.Assign(sysutil.CoreSchedScopeThread, pids[0], sysutil.CoreSchedScopeThreadGroup, pids[1:]...)
	if err != nil {
		klog.V(5).Infof("failed to assign new cookie for group %s, cookie %v, PID from %v, PID to %v failed of %v, err: %s",
			groupID, cookieID, pids[0], len(failedPIDs), len(pids)-1, err)
	}

	pidsAdded := NewPIDCache(pids...)
	pidsAdded.DeleteAny(failedPIDs...)

	return cookieID, pidsAdded.GetAllSorted(), nil
}

// assignCookie assigns the target cookieID to the given PIDs.
// It returns the PIDs assigned, PIDs to delete, and the error (when exists, fallback adding new cookie).
// TODO: refactor to resource updater.
func (p *Plugin) assignCookie(pids, siblingPIDs []uint32, groupID string, targetCookieID uint64) ([]uint32, []uint32, error) {
	if len(pids) <= 0 {
		klog.V(6).Infof("aborted to assign PIDs cookie for group %v, target cookie %v, no PID", groupID, targetCookieID)
		return nil, nil, nil
	}
	pidsToAssign := NewPIDCache()
	var pidsAssigned []uint32
	unknownCount := 0
	for _, pid := range pids {
		lastCookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pid)
		if err != nil {
			klog.V(6).Infof("failed to get cookie for PID %v during assign, group %s, err: %s",
				pid, groupID, err)
			unknownCount++
			continue
		}
		if lastCookieID != targetCookieID {
			pidsToAssign.AddAny(pid)
		} else {
			pidsAssigned = append(pidsAssigned, pid)
		}
	}

	if unknownCount >= len(pids) { // in case the given pids terminate, e.g. the container is restarting, aborted
		klog.V(5).Infof("failed to get last cookie for group %s, got %v unknown of %v PIDs",
			groupID, unknownCount, len(pids))
		return nil, nil, nil
	}

	if pidsToAssign.Len() <= 0 { // all PIDs are assigned, just refresh reference
		return pidsAssigned, nil, nil
	}

	var sPIDsToDelete []uint32
	validSiblingPID := uint32(0) // find one valid sibling PID to share from
	for _, sPID := range siblingPIDs {
		pCookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, sPID)
		if err != nil {
			klog.V(6).Infof("failed to get cookie for sibling PID %v, group %s, err: %s",
				sPID, groupID, err)
			sPIDsToDelete = append(sPIDsToDelete, sPID)
			continue
		}
		if pCookieID != targetCookieID {
			klog.V(6).Infof("failed to get target cookie for sibling PID %v, err: expect %v but got %v",
				sPID, targetCookieID, pCookieID)
			sPIDsToDelete = append(sPIDsToDelete, sPID)
			continue
		}

		// get the first valid sibling PID
		validSiblingPID = sPID
		break
	}

	if validSiblingPID == 0 {
		return nil, sPIDsToDelete, fmt.Errorf("no valid sibling PID, sibling PIDs to delete num %v",
			len(sPIDsToDelete))
	}

	// assign to valid sibling PID
	failedPIDs, err := p.cse.Assign(sysutil.CoreSchedScopeThread, validSiblingPID, sysutil.CoreSchedScopeThreadGroup, pidsToAssign.GetAllSorted()...)
	if err != nil {
		klog.V(5).Infof("failed to assign group cookie for group %s, target cookie %v, PID from %v, PID to %v failed of %v, err: %s",
			groupID, targetCookieID, validSiblingPID, len(failedPIDs), pidsToAssign.Len(), err)
		pidsToAssign.DeleteAny(failedPIDs...)
	}
	pidsToAssign.AddAny(pidsAssigned...)

	return pidsToAssign.GetAllSorted(), sPIDsToDelete, nil
}

// clearCookie clears the cookie for the given PIDs to the default cookie 0.
// It returns the PIDs cleared.
func (p *Plugin) clearCookie(pids []uint32, groupID string, lastCookieID uint64) []uint32 {
	if len(pids) <= 0 {
		klog.V(6).Infof("aborted to clear PIDs cookie for group %s, no PID", groupID)
		return nil
	}
	pidsToClear := NewPIDCache()
	var pidsCleared []uint32
	for _, pid := range pids {
		pCookieID, err := p.cse.Get(sysutil.CoreSchedScopeThread, pid)
		if err != nil {
			klog.V(6).Infof("failed to get cookie for PID %v, group %s, err: %s", pid, groupID, err)
			continue
		}
		if pCookieID != sysutil.DefaultCoreSchedCookieID {
			pidsToClear.AddAny(pid)
		} else {
			pidsCleared = append(pidsCleared, pid)
		}
	}

	if pidsToClear.Len() <= 0 {
		return pidsCleared
	}

	failedPIDs, err := p.cse.Clear(sysutil.CoreSchedScopeThreadGroup, pidsToClear.GetAllSorted()...)
	if err != nil {
		klog.V(4).Infof("failed to clear cookie for group %v, last cookie %v, PID %v failed of %v, total %v, err: %s",
			groupID, lastCookieID, len(failedPIDs), pidsToClear.GetAllSorted(), len(pids), err)
		pidsToClear.DeleteAny(failedPIDs...)
	}
	pidsToClear.AddAny(pidsCleared...)

	return pidsToClear.GetAllSorted()
}

// getPodEnabledAndGroup gets whether the pod enables the core scheduling and the group ID if it does.
func (p *Plugin) getPodEnabledAndGroup(podAnnotations, podLabels map[string]string, podKubeQOS corev1.PodQOSClass, podUID string) (bool, string) {
	groupID := slov1alpha1.GetCoreSchedGroupID(podLabels)
	policy := slov1alpha1.GetCoreSchedPolicy(podLabels)
	podQOS := extension.QoSNone
	if podLabels != nil {
		podQOS = extension.GetQoSClassByAttrs(podLabels, podAnnotations)
	}
	isEnabled, isExpeller := p.rule.IsPodEnabled(podQOS, podKubeQOS)

	if policy == slov1alpha1.CoreSchedPolicyExclusive {
		groupID = podUID
	} else if policy == slov1alpha1.CoreSchedPolicyNone {
		isEnabled = false
	}
	if isExpeller {
		groupID += ExpellerGroupSuffix
	}

	return isEnabled, groupID
}

func (p *Plugin) getContainerUID(podUID string, containerID string) string {
	return podUID + "/" + containerID
}

func (p *Plugin) getContainerPIDs(containerCgroupParent string) ([]uint32, error) {
	pids, err := p.reader.ReadCPUProcs(containerCgroupParent)
	if err != nil && resourceexecutor.IsCgroupDirErr(err) {
		klog.V(5).Infof("aborted to get PIDs for container dir %s, err: %s",
			containerCgroupParent, err)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get container PIDs failed, err: %w", err)
	}
	return pids, nil
}

func (p *Plugin) getSandboxContainerPIDs(podMeta *statesinformer.PodMeta) ([]uint32, string, error) {
	sandboxID, err := util.GetPodSandboxContainerID(podMeta.Pod)
	if err != nil {
		return nil, "", fmt.Errorf("get sandbox container ID failed, err: %w", err)
	}
	sandboxContainerDir, err := util.GetContainerCgroupParentDirByID(podMeta.CgroupDir, sandboxID)
	if err != nil {
		return nil, sandboxID, fmt.Errorf("get cgroup parent for sandbox container %s/%s, err: %w",
			podMeta.Key(), sandboxID, err)
	}
	pids, err := p.getContainerPIDs(sandboxContainerDir)
	if err != nil {
		return nil, sandboxID, fmt.Errorf("get PID failed for sandbox container %s/%s, parent dir %s, err: %w",
			podMeta.Key(), sandboxID, sandboxContainerDir, err)
	}
	return pids, sandboxID, nil
}

func (p *Plugin) getNormalContainerPIDs(podMeta *statesinformer.PodMeta, containerStatus *corev1.ContainerStatus) ([]uint32, error) {
	var pids []uint32
	containerDir, err := util.GetContainerCgroupParentDir(podMeta.CgroupDir, containerStatus)
	if err != nil {
		return nil, fmt.Errorf("get cgroup parent for container %s/%s, err: %w",
			podMeta.Key(), containerStatus.Name, err)
	}
	pids, err = p.getContainerPIDs(containerDir)
	if err != nil {
		return nil, fmt.Errorf("get PID failed for container %s/%s, parent dir %s, err: %w",
			podMeta.Key(), containerStatus.Name, containerDir, err)
	}
	return pids, nil
}

func (p *Plugin) getAllContainerPIDs(podMeta *statesinformer.PodMeta) []*containerPID {
	var containerToPIDs []*containerPID
	count := 0
	pod := podMeta.Pod

	// for sandbox container
	sandboxPIDs, sandboxContainerID, err := p.getSandboxContainerPIDs(podMeta)
	if err != nil {
		klog.V(5).Infof("failed to get sandbox container PID for pod %s, err: %s", podMeta.Key(), err)
	} else {
		containerToPIDs = append(containerToPIDs, &containerPID{
			ContainerID: sandboxContainerID,
			PID:         sandboxPIDs,
		})
		count += len(sandboxPIDs)
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

		containerPIDs, err := p.getNormalContainerPIDs(podMeta, containerStat)
		if err != nil {
			klog.V(5).Infof("failed to get container %s PID for pod %s, err: %s",
				container.Name, podMeta.Key(), err)
			continue
		}

		containerToPIDs = append(containerToPIDs, &containerPID{
			ContainerName: containerStat.Name,
			ContainerID:   containerStat.ContainerID,
			PID:           containerPIDs,
		})
		count += len(containerPIDs)
	}

	klog.V(6).Infof("get PIDs for pod %s finished, sandbox and container num %v, PID num %v",
		podMeta.Key(), len(containerToPIDs), count)
	return containerToPIDs
}

func recordContainerCookieMetrics(containerCtx *protocol.ContainerContext, groupID string, cookieID uint64) {
	metrics.RecordContainerCoreSchedCookie(containerCtx.Request.PodMeta.Namespace,
		containerCtx.Request.PodMeta.Name, containerCtx.Request.PodMeta.UID,
		containerCtx.Request.ContainerMeta.Name, containerCtx.Request.ContainerMeta.ID,
		groupID, cookieID)
	metrics.RecordCoreSchedCookieManageStatus(groupID, true)
}

func resetContainerCookieMetrics(containerCtx *protocol.ContainerContext, groupID string, lastCookieID uint64) {
	metrics.ResetContainerCoreSchedCookie(containerCtx.Request.PodMeta.Namespace,
		containerCtx.Request.PodMeta.Name, containerCtx.Request.PodMeta.UID,
		containerCtx.Request.ContainerMeta.Name, containerCtx.Request.ContainerMeta.ID,
		groupID, lastCookieID)
}
