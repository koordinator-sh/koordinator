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

package groupidentity

import (
	"fmt"
	"reflect"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

type bvtRule struct {
	enable           bool
	podQOSParams     map[ext.QoSClass]int64
	kubeQOSDirParams map[corev1.PodQOSClass]int64
	kubeQOSPodParams map[corev1.PodQOSClass]int64
}

func (r *bvtRule) getEnable() bool {
	if r == nil {
		return false
	}
	return r.enable
}

func (r *bvtRule) getPodBvtValue(podQoSClass ext.QoSClass, podKubeQOS corev1.PodQOSClass) int64 {
	if val, exist := r.podQOSParams[podQoSClass]; exist {
		return val
	}
	if val, exist := r.kubeQOSPodParams[podKubeQOS]; exist {
		return val
	}
	return *sloconfig.NoneCPUQOS().GroupIdentity
}

func (r *bvtRule) getKubeQOSDirBvtValue(kubeQOS corev1.PodQOSClass) int64 {
	if bvtValue, exist := r.kubeQOSDirParams[kubeQOS]; exist {
		return bvtValue
	}
	return *sloconfig.NoneCPUQOS().GroupIdentity
}

func (r *bvtRule) getHostQOSBvtValue(qosClass ext.QoSClass) int64 {
	if val, exist := r.podQOSParams[qosClass]; exist {
		return val
	}
	return *sloconfig.NoneCPUQOS().GroupIdentity
}

func (b *bvtPlugin) parseRule(mergedNodeSLOIf interface{}) (bool, error) {
	mergedNodeSLO := mergedNodeSLOIf.(*slov1alpha1.NodeSLOSpec)
	qosStrategy := mergedNodeSLO.ResourceQOSStrategy

	// default policy enables
	isPolicyGroupIdentity := qosStrategy.Policies == nil || qosStrategy.Policies.CPUPolicy == nil ||
		len(*qosStrategy.Policies.CPUPolicy) <= 0 || *qosStrategy.Policies.CPUPolicy == slov1alpha1.CPUQOSPolicyGroupIdentity
	// check if bvt (group identity) is enabled
	lsrEnabled := isPolicyGroupIdentity && *qosStrategy.LSRClass.CPUQOS.Enable
	lsEnabled := isPolicyGroupIdentity && *qosStrategy.LSClass.CPUQOS.Enable
	beEnabled := isPolicyGroupIdentity && *qosStrategy.BEClass.CPUQOS.Enable

	// setting pod rule by qos config
	// Group Identity should be reset if the CPU QOS disables (already merged in states informer) or the CPU QoS policy
	// is not "groupIdentity".
	lsrValue := *sloconfig.NoneCPUQOS().GroupIdentity
	if lsrEnabled {
		lsrValue = *qosStrategy.LSRClass.CPUQOS.GroupIdentity
	}
	lsValue := *sloconfig.NoneCPUQOS().GroupIdentity
	if lsEnabled {
		lsValue = *qosStrategy.LSClass.CPUQOS.GroupIdentity
	}
	beValue := *sloconfig.NoneCPUQOS().GroupIdentity
	if beEnabled {
		beValue = *qosStrategy.BEClass.CPUQOS.GroupIdentity
	}

	// setting besteffort according to BE
	besteffortDirVal := beValue
	besteffortPodVal := beValue

	// setting burstable according to LS
	burstableDirVal := lsValue
	burstablePodVal := lsValue

	// NOTE: guaranteed root dir must set as 0 until kernel supported
	guaranteedDirVal := *sloconfig.NoneCPUQOS().GroupIdentity
	// setting guaranteed pod enabled if LS or LSR enabled
	guaranteedPodVal := *sloconfig.NoneCPUQOS().GroupIdentity
	if lsrEnabled {
		guaranteedPodVal = lsrValue
	} else if lsEnabled {
		guaranteedPodVal = lsValue
	}

	newRule := &bvtRule{
		enable: lsrEnabled || lsEnabled || beEnabled,
		podQOSParams: map[ext.QoSClass]int64{
			ext.QoSLSE: lsrValue,
			ext.QoSLSR: lsrValue,
			ext.QoSLS:  lsValue,
			ext.QoSBE:  beValue,
		},
		kubeQOSDirParams: map[corev1.PodQOSClass]int64{
			corev1.PodQOSGuaranteed: guaranteedDirVal,
			corev1.PodQOSBurstable:  burstableDirVal,
			corev1.PodQOSBestEffort: besteffortDirVal,
		},
		kubeQOSPodParams: map[corev1.PodQOSClass]int64{
			corev1.PodQOSGuaranteed: guaranteedPodVal,
			corev1.PodQOSBurstable:  burstablePodVal,
			corev1.PodQOSBestEffort: besteffortPodVal,
		},
	}

	updated := b.updateRule(newRule)
	klog.Infof("runtime hook plugin %s update rule %v, new rule %v", name, updated, newRule)
	return updated, nil
}

func (b *bvtPlugin) ruleUpdateCb(target *statesinformer.CallbackTarget) error {
	if !b.SystemSupported() {
		klog.V(5).Infof("plugin %s is not supported by system", name)
		return nil
	}
	r := b.getRule()
	if r == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return nil
	}

	// check sysctl
	// Currently, the kernel feature core scheduling is conflict to the group identity. So before we enable the
	// group identity, we should check if the GroupIdentity can be enabled via sysctl and the CoreSched can be
	// disabled via sysctl. And when we disable the group identity, we can check if the GroupIdentity is already
	// disabled which means we do not need to update the cgroups.
	isEnabled := r.getEnable()
	if isEnabled {
		if err := b.initSysctl(); err != nil {
			klog.Warningf("failed to initialize system config for plugin %s, err: %s", name, err)
			return nil
		}
	} else {
		isSysctlEnabled, err := b.isSysctlEnabled()
		if err != nil {
			klog.Warningf("failed to check sysctl for plugin %s, err: %s", name, err)
			return nil
		}
		if !r.getEnable() && !isSysctlEnabled { // no need to update cgroups if both rule and sysctl disabled
			klog.V(4).Infof("rule is disabled for plugin %v, no more to do for resources", name)
			return nil
		}
	}

	qosCgroupMap := map[string]struct{}{}
	for _, kubeQOS := range []corev1.PodQOSClass{
		corev1.PodQOSGuaranteed, corev1.PodQOSBurstable, corev1.PodQOSBestEffort} {
		bvtValue := r.getKubeQOSDirBvtValue(kubeQOS)
		kubeQOSCgroupPath := koordletutil.GetPodQoSRelativePath(kubeQOS)
		e := audit.V(3).Group(string(kubeQOS)).Reason(name).Message("set bvt to %v", bvtValue)
		bvtUpdater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUBVTWarpNsName, kubeQOSCgroupPath, strconv.FormatInt(bvtValue, 10), e)
		if err != nil {
			klog.Infof("bvt updater create failed, dir %v, error %v", kubeQOSCgroupPath, err)
			continue
		}
		if _, err = b.executor.Update(true, bvtUpdater); err != nil {
			klog.Infof("update kube qos %v cpu bvt failed, dir %v, error %v", kubeQOS, kubeQOSCgroupPath, err)
		}
		qosCgroupMap[kubeQOSCgroupPath] = struct{}{}
	}

	if target == nil {
		return fmt.Errorf("callback target is nil")
	}

	// FIXME(saintube): Currently the kernel feature core scheduling is strictly excluded with the group identity's
	//   bvt=-1. So we have to check and disable all the BE cgroups' bvt for the GroupIdentity before creating the
	//   core sched cookies. To keep the consistency of the cgroup tree's configuration, we list and update the cgroups
	//   by the level order when the group identity is globally disabled.
	//   This check should be removed after the kernel provides a more stable interface.
	podCgroupMap := map[string]int64{}
	// pod-level
	for _, kubeQOS := range []corev1.PodQOSClass{corev1.PodQOSGuaranteed, corev1.PodQOSBurstable, corev1.PodQOSBestEffort} {
		bvtValue := r.getKubeQOSDirBvtValue(kubeQOS)
		kubeQOSParentDir := koordletutil.GetPodQoSRelativePath(kubeQOS)
		podCgroupDirs, err := koordletutil.GetCgroupPathsByTargetDepth(sysutil.CPUBVTWarpNsName, kubeQOSParentDir, koordletutil.PodCgroupPathRelativeDepth)
		if err != nil {
			klog.Infof("get pod cgroup paths failed, qos %s, err: %w", kubeQOS, err)
			continue
		}
		for _, cgroupDir := range podCgroupDirs {
			if _, ok := qosCgroupMap[cgroupDir]; ok { // exclude qos cgroup
				continue
			}
			podCgroupMap[cgroupDir] = bvtValue
		}
	}
	for _, podMeta := range target.Pods {
		podQOS := ext.GetPodQoSClassRaw(podMeta.Pod)
		podKubeQOS := podMeta.Pod.Status.QOSClass
		podBvt := r.getPodBvtValue(podQOS, podKubeQOS)
		podCgroupPath := podMeta.CgroupDir
		e := audit.V(3).Pod(podMeta.Pod.Namespace, podMeta.Pod.Name).Reason(name).Message("set bvt to %v", podBvt)
		bvtUpdater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUBVTWarpNsName, podCgroupPath, strconv.FormatInt(podBvt, 10), e)
		if err != nil {
			klog.Infof("bvt updater create failed, dir %v, error %v", podCgroupPath, err)
			continue
		}
		if _, err = b.executor.Update(true, bvtUpdater); err != nil {
			klog.Infof("update pod %s cpu bvt failed, dir %v, error %v",
				util.GetPodKey(podMeta.Pod), podCgroupPath, err)
		}
		delete(podCgroupMap, podCgroupPath)

		// container-level
		// NOTE: Although we do not set the container's cpu.bvt_warp_ns directly, it is inheritable from the pod-level,
		//       we have to handle the container-level only when we want to disable the group identity.
		containerCgroupDirs, err := koordletutil.GetCgroupPathsByTargetDepth(sysutil.CPUBVTWarpNsName, podCgroupPath, 1)
		if err != nil {
			klog.Infof("get container cgroup paths failed, dir %s, error %v", podCgroupPath, err)
			continue
		}
		for _, cgroupDir := range containerCgroupDirs {
			bvtUpdater, err = resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUBVTWarpNsName, cgroupDir, strconv.FormatInt(podBvt, 10), e)
			if err != nil {
				klog.Infof("bvt updater create failed, dir %v, error %v", cgroupDir, err)
				continue
			}
			if _, err = b.executor.Update(true, bvtUpdater); err != nil {
				klog.Infof("update container cpu bvt failed, dir %v, error %v", cgroupDir, err)
			}
		}
	}
	for _, hostApp := range target.HostApplications {
		hostCtx := protocol.HooksProtocolBuilder.HostApp(&hostApp)
		if err := b.SetHostAppBvtValue(hostCtx); err != nil {
			klog.Warningf("set host application %v bvt value failed, error %v", hostApp.Name, err)
		} else {
			hostCtx.ReconcilerDone(b.executor)
			klog.V(5).Infof("set host application %v bvt value finished", hostApp.Name)
		}
	}
	// handle the remaining pod cgroups, which can belong to the dangling pods
	for podCgroupDir, bvtValue := range podCgroupMap {
		e := audit.V(3).Reason(name).Message("set bvt to %v", bvtValue)
		bvtUpdater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUBVTWarpNsName, podCgroupDir, strconv.FormatInt(bvtValue, 10), e)
		if err != nil {
			klog.Infof("bvt updater create failed, dir %v, error %v", podCgroupDir, err)
			continue
		}
		if _, err = b.executor.Update(true, bvtUpdater); err != nil {
			klog.Infof("update remaining pod cpu bvt failed, dir %v, error %v", podCgroupDir, err)
		}

		// container-level
		containerCgroupDirs, err := koordletutil.GetCgroupPathsByTargetDepth(sysutil.CPUBVTWarpNsName, podCgroupDir, 1)
		if err != nil {
			klog.Infof("get container cgroup paths failed, dir %s, error %v", podCgroupDir, err)
			continue
		}
		for _, cgroupDir := range containerCgroupDirs {
			bvtUpdater, err = resourceexecutor.DefaultCgroupUpdaterFactory.New(sysutil.CPUBVTWarpNsName, cgroupDir, strconv.FormatInt(bvtValue, 10), e)
			if err != nil {
				klog.Infof("bvt updater create failed, dir %v, error %v", cgroupDir, err)
				continue
			}
			if _, err = b.executor.Update(true, bvtUpdater); err != nil {
				klog.Infof("update remaining container cpu bvt failed, dir %v, error %v", cgroupDir, err)
			}
		}
	}

	return nil
}

func (b *bvtPlugin) getRule() *bvtRule {
	b.ruleRWMutex.RLock()
	defer b.ruleRWMutex.RUnlock()
	if b.rule == nil {
		return nil
	}
	rule := *b.rule
	return &rule
}

func (b *bvtPlugin) updateRule(newRule *bvtRule) bool {
	b.ruleRWMutex.Lock()
	defer b.ruleRWMutex.Unlock()
	if !reflect.DeepEqual(newRule, b.rule) {
		b.rule = newRule
		return true
	}
	return false
}
