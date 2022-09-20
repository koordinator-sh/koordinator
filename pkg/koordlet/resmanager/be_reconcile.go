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
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/executor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func (r *resmanager) reconcileBECgroup() {
	nodeSLO := r.getNodeSLOCopy()
	podsMeta := r.statesInformer.GetAllPods()
	for _, podMeta := range podsMeta {
		if extension.GetPodQoSClass(podMeta.Pod) != extension.QoSBE {
			continue
		}
		if enable, policy := getCPUSuppressPolicy(nodeSLO); !enable || policy != v1alpha1.CPUCfsQuotaPolicy {
			reconcileBECPULimit(podMeta)
		}
		reconcileBECPUShare(podMeta)
		reconcileBEMemLimit(podMeta)
	}
}

func reconcileBECPULimit(podMeta *statesinformer.PodMeta) {
	needReconcilePod, err := needReconcilePodBECPULimit(podMeta)
	if err != nil {
		klog.Warningf("failed to check need reconcile cpu limit for pod %v/%v %v, error: %v",
			podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, err)
		return
	}
	if needReconcilePod {
		err = applyPodBECPULimitIfSpecified(podMeta)
		if err != nil {
			klog.Warningf("failed to apply cpu limit for pod %v/%v %v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, err)
		} else {
			curCFS, err := util.GetPodCurCFSQuota(podMeta.CgroupDir)
			klog.Infof("apply cpu limit for pod %v/%v %v succeed, current value %d, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, curCFS, err)
		}
	}

	containerMap := make(map[string]*corev1.Container, len(podMeta.Pod.Spec.Containers))
	for i := range podMeta.Pod.Spec.Containers {
		container := &podMeta.Pod.Spec.Containers[i]
		containerMap[container.Name] = container
	}

	for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
		container, exist := containerMap[containerStat.Name]
		if !exist {
			klog.Warningf("container %v/%v/%v lost during cpu limit reconcile",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name)
			continue
		}
		needReconcileContainer, err := needReconcileContainerBECPULimit(podMeta, container, &containerStat)
		if err != nil {
			klog.Warningf("failed to check need reconcile cpu limit for container %v/%v/%v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name, err)
			continue
		}
		if !needReconcileContainer {
			continue
		}

		if err := applyContainerBECPULimitIfSpecified(podMeta, container, &containerStat); err != nil {
			klog.Warningf("failed to apply cpu limit for container %v/%v/%v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name, err)
		} else {
			curCFS, err := util.GetContainerCurCFSQuota(podMeta.CgroupDir, &containerStat)
			klog.Infof("apply cpu limit for container %v/%v %v succeed, current value %v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, curCFS, err)
		}
	}
}

func reconcileBECPUShare(podMeta *statesinformer.PodMeta) {
	needReconcilePod, err := needReconcilePodBECPUShare(podMeta)
	if err != nil {
		klog.Warningf("failed to check need reconcile cpu request for pod %v/%v %v, error: %v",
			podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, err)
		return
	}
	if needReconcilePod {
		err = applyPodBECPURequestIfSpecified(podMeta)
		if err != nil {
			klog.Warningf("failed to apply cpu request for pod %v/%v %v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, err)
		} else {
			curShare, err := util.GetPodCurCPUShare(podMeta.CgroupDir)
			klog.Infof("apply cpu request for pod %v/%v %v succeed, current value %d, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, curShare, err)
		}
	}

	containerMap := make(map[string]*corev1.Container, len(podMeta.Pod.Spec.Containers))
	for i := range podMeta.Pod.Spec.Containers {
		container := &podMeta.Pod.Spec.Containers[i]
		containerMap[container.Name] = container
	}
	for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
		// reconcile containers
		container, exist := containerMap[containerStat.Name]
		if !exist {
			klog.Warningf("container %v/%v/%v lost during reconcile cpu request",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name)
			continue
		}
		needReconcileContainer, err := needReconcileContainerBECPUShare(podMeta, container, &containerStat)
		if err != nil {
			klog.Warningf("failed to check need reconcile cpu request for container %v/%v/%v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name, err)
			continue
		}
		if !needReconcileContainer {
			continue
		}
		err = applyContainerBECPUShareIfSpecified(podMeta, container, &containerStat)
		if err != nil {
			klog.Warningf("failed to apply cpu request for container %v/%v/%v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name, err)
		} else {
			curShare, err := util.GetContainerCurCPUShare(podMeta.CgroupDir, &containerStat)
			klog.Infof("apply cpu request for pod %v/%v %v succeed, current value %d, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name, curShare, err)
		}
	}
}

func reconcileBEMemLimit(podMeta *statesinformer.PodMeta) {
	needReconcilePod, err := needReconcilePodBEMemLimit(podMeta)
	if err != nil {
		klog.Warningf("failed to check need reconcile memory limit for pod %v/%v %v, error: %v",
			podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, err)
		return
	}
	if needReconcilePod {
		err = applyPodBEMemLimitIfSpecified(podMeta)
		if err != nil {
			klog.Warningf("failed to apply cpu memory for pod %v/%v %v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, err)
		} else {
			curLimit, err := util.GetPodCurMemLimitBytes(podMeta.CgroupDir)
			klog.Infof("apply cpu memory for pod %v/%v %v succeed, current value %d, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, curLimit, err)
		}
	}

	containerMap := make(map[string]*corev1.Container, len(podMeta.Pod.Spec.Containers))
	for i := range podMeta.Pod.Spec.Containers {
		container := &podMeta.Pod.Spec.Containers[i]
		containerMap[container.Name] = container
	}

	for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
		container, exist := containerMap[containerStat.Name]
		if !exist {
			klog.Warningf("container %v/%v/%v lost during memory limit reconcile",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name)
			continue
		}
		needReconcileContainer, err := needReconcileContainerBEMemLimit(podMeta, container, &containerStat)
		if err != nil {
			klog.Warningf("failed to check need reconcile memory limit for container %v/%v/%v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name, err)
			continue
		}
		if !needReconcileContainer {
			continue
		}

		if err := applyContainerBEMemLimitIfSpecified(podMeta, container, &containerStat); err != nil {
			klog.Warningf("failed to apply memory limit for container %v/%v/%v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, containerStat.Name, err)
		} else {
			curLimit, err := util.GetContainerCurMemLimitBytes(podMeta.CgroupDir, &containerStat)
			klog.Infof("apply memory limit for container %v/%v %v succeed, current value %v, error: %v",
				podMeta.Pod.Namespace, podMeta.Pod.Name, podMeta.Pod.UID, curLimit, err)
		}
	}
}

func needReconcilePodBECPULimit(podMeta *statesinformer.PodMeta) (bool, error) {
	if util.GetPodBEMilliCPULimit(podMeta.Pod) <= 0 {
		return false, nil
	}

	podCurCFSQuota, err := util.GetPodCurCFSQuota(podMeta.CgroupDir)
	if err != nil {
		return false, err
	}
	return podCurCFSQuota == system.CFSQuotaUnlimitedValue, nil
}

func needReconcileContainerBECPULimit(podMeta *statesinformer.PodMeta, container *corev1.Container, containerStatus *corev1.ContainerStatus) (bool, error) {
	if util.GetContainerBatchMilliCPULimit(container) <= 0 {
		return false, nil
	}

	containerCurCFSQuota, err := util.GetContainerCurCFSQuota(podMeta.CgroupDir, containerStatus)
	if err != nil {
		return false, err
	}
	return containerCurCFSQuota == system.CFSQuotaUnlimitedValue, nil
}

func needReconcilePodBECPUShare(podMeta *statesinformer.PodMeta) (bool, error) {
	if util.GetPodBEMilliCPURequest(podMeta.Pod) <= 0 {
		return false, nil
	}

	podCurCPUShare, err := util.GetPodCurCPUShare(podMeta.CgroupDir)
	if err != nil {
		return false, err
	}
	return podCurCPUShare == system.CPUShareKubeBEValue, nil
}

func needReconcileContainerBECPUShare(podMeta *statesinformer.PodMeta, container *corev1.Container, containerStatus *corev1.ContainerStatus) (bool, error) {
	if util.GetContainerBatchMilliCPURequest(container) <= 0 {
		return false, nil
	}

	containerCurCPUShare, err := util.GetContainerCurCPUShare(podMeta.CgroupDir, containerStatus)
	if err != nil {
		return false, err
	}
	return containerCurCPUShare == system.CPUShareKubeBEValue, nil
}

func needReconcilePodBEMemLimit(podMeta *statesinformer.PodMeta) (bool, error) {
	if util.GetPodBEMemoryByteLimit(podMeta.Pod) <= 0 {
		return false, nil
	}

	podCurMemLimit, err := util.GetPodCurMemLimitBytes(podMeta.CgroupDir)
	if err != nil {
		return false, err
	}
	// by default 9223372036854771712 , use 1024TB = 1024 * 1024 * 1024 * 1024 * 1024 for compatibility
	return podCurMemLimit > 1024*1024*1024*1024*1024, nil
}

func needReconcileContainerBEMemLimit(podMeta *statesinformer.PodMeta, container *corev1.Container, containerStatus *corev1.ContainerStatus) (bool, error) {
	if util.GetContainerBatchMemoryByteLimit(container) <= 0 {
		return false, nil
	}

	containerCurCFSQuota, err := util.GetContainerCurMemLimitBytes(podMeta.CgroupDir, containerStatus)
	if err != nil {
		return false, err
	}
	// by default 9223372036854771712 , use 1024TB = 1024 * 1024 * 1024 * 1024 * 1024 for compatibility
	return containerCurCFSQuota > 1024*1024*1024*1024*1024, nil
}

func applyPodBECPULimitIfSpecified(podMeta *statesinformer.PodMeta) error {
	milliCPULimit := util.GetPodBEMilliCPULimit(podMeta.Pod)
	if milliCPULimit <= 0 {
		return nil
	}
	podCFSPeriod, err := util.GetPodCurCFSPeriod(podMeta.CgroupDir)
	if err != nil {
		return err
	}
	targetCFSQuota := int(float64(milliCPULimit*podCFSPeriod) / float64(1000))
	podCFSQuotaPath := util.GetPodCgroupCFSQuotaPath(podMeta.CgroupDir)
	_ = audit.V(2).Pod(podMeta.Pod.Namespace, podMeta.Pod.Name).Reason(executor.UpdateCPU).Message("set cfs_quota to %v", targetCFSQuota).Do()
	return os.WriteFile(podCFSQuotaPath, []byte(strconv.Itoa(targetCFSQuota)), 0644)
}

func applyContainerBECPULimitIfSpecified(podMeta *statesinformer.PodMeta, container *corev1.Container, containerStatus *corev1.ContainerStatus) error {
	milliCPULimit := util.GetContainerBatchMilliCPULimit(container)
	if milliCPULimit <= 0 {
		return nil
	}
	containerCFSPeriod, err := util.GetContainerCurCFSPeriod(podMeta.CgroupDir, containerStatus)
	if err != nil {
		return err
	}
	targetCFSQuota := int(float64(milliCPULimit*containerCFSPeriod) / float64(1000))
	containerCFSQuotaPath, err := util.GetContainerCgroupCFSQuotaPath(podMeta.CgroupDir, containerStatus)
	if err != nil {
		return err
	}
	_ = audit.V(2).Pod(podMeta.Pod.Namespace, podMeta.Pod.Name).Container(container.Name).Reason(executor.UpdateCPU).Message("set cfs_quota to %v", targetCFSQuota).Do()
	return os.WriteFile(containerCFSQuotaPath, []byte(strconv.Itoa(targetCFSQuota)), 0644)
}

func applyPodBECPURequestIfSpecified(podMeta *statesinformer.PodMeta) error {
	milliCPURequest := util.GetPodBEMilliCPURequest(podMeta.Pod)
	if milliCPURequest <= 0 {
		return nil
	}
	targetCPUShare := int(float64(milliCPURequest*system.CPUShareUnitValue) / float64(1000))
	podDir := util.GetPodCgroupDirWithKube(podMeta.CgroupDir)
	_ = audit.V(2).Pod(podMeta.Pod.Namespace, podMeta.Pod.Name).Reason(executor.UpdateCPU).Message("set cfs_shares to %v", targetCPUShare).Do()
	return system.CgroupFileWrite(podDir, system.CPUShares, strconv.Itoa(targetCPUShare))
}

func applyContainerBECPUShareIfSpecified(podMeta *statesinformer.PodMeta, container *corev1.Container, containerStatus *corev1.ContainerStatus) error {
	milliCPURequest := util.GetContainerBatchMilliCPURequest(container)
	if milliCPURequest <= 0 {
		return nil
	}
	targetCPUShare := int(float64(milliCPURequest*system.CPUShareUnitValue) / float64(1000))
	containerCPUSharePath, err := util.GetContainerCgroupCPUSharePath(podMeta.CgroupDir, containerStatus)
	if err != nil {
		return err
	}
	_ = audit.V(2).Pod(podMeta.Pod.Namespace, podMeta.Pod.Name).Container(container.Name).Reason(executor.UpdateCPU).Message("set cfs_shares to %v", targetCPUShare).Do()
	return os.WriteFile(containerCPUSharePath, []byte(strconv.Itoa(targetCPUShare)), 0644)
}

func applyPodBEMemLimitIfSpecified(podMeta *statesinformer.PodMeta) error {
	memoryLimit := util.GetPodBEMemoryByteLimit(podMeta.Pod)
	if memoryLimit <= 0 {
		return nil
	}
	podMemLimitPath := util.GetPodCgroupMemLimitPath(podMeta.CgroupDir)
	_ = audit.V(2).Pod(podMeta.Pod.Namespace, podMeta.Pod.Name).Reason(executor.UpdateMemory).Message("set memory.limits to %v", memoryLimit).Do()
	return os.WriteFile(podMemLimitPath, []byte(strconv.Itoa(int(memoryLimit))), 0644)
}

func applyContainerBEMemLimitIfSpecified(podMeta *statesinformer.PodMeta, container *corev1.Container, containerStatus *corev1.ContainerStatus) error {
	memoryLimit := util.GetContainerBatchMemoryByteLimit(container)
	if memoryLimit <= 0 {
		return nil
	}
	containerMemLimitPath, err := util.GetContainerCgroupMemLimitPath(podMeta.CgroupDir, containerStatus)
	if err != nil {
		return err
	}
	_ = audit.V(2).Pod(podMeta.Pod.Namespace, podMeta.Pod.Name).Container(container.Name).Reason(executor.UpdateMemory).Message("set memory.limits to %v", memoryLimit).Do()
	return os.WriteFile(containerMemLimitPath, []byte(strconv.Itoa(int(memoryLimit))), 0644)
}
