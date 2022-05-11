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

package util

import (
	"fmt"
	"io/ioutil"
	"path"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/kubernetes/pkg/apis/core/v1/helper/qos"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

// @kubeRelativeDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @return kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
func GetPodCgroupDirWithKube(podKubeRelativeDir string) string {
	return path.Join(system.CgroupPathFormatter.ParentDir, podKubeRelativeDir)
}

// @return like kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
func GetPodKubeRelativePath(pod *corev1.Pod) string {
	qosClass := GetKubeQosClass(pod)
	return path.Join(
		system.CgroupPathFormatter.QOSDirFn(qosClass),
		system.CgroupPathFormatter.PodDirFn(qosClass, string(pod.UID)),
	)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpuacct/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpuacct.proc_stat
func GetPodCgroupCPUAcctProcStatPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return system.GetCgroupFilePath(podPath, system.CpuacctStat)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpu/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpu.shares
func GetPodCgroupCPUSharePath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return system.GetCgroupFilePath(podPath, system.CPUShares)
}

func GetPodCgroupCFSPeriodPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return system.GetCgroupFilePath(podPath, system.CPUCFSPeriod)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpu/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpu.cfs_quota_us
func GetPodCgroupCFSQuotaPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return system.GetCgroupFilePath(podPath, system.CPUCFSQuota)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpuacct/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpuacct.proc_stat
func GetPodCgroupMemStatPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return system.GetCgroupFilePath(podPath, system.MemStat)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/memory/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/memory.limit_in_bytes
func GetPodCgroupMemLimitPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return system.GetCgroupFilePath(podPath, system.MemoryLimit)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpu/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpu.stat
func GetPodCgroupCPUStatPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return system.GetCgroupFilePath(podPath, system.CPUStat)
}

func GetKubeQosClass(pod *corev1.Pod) corev1.PodQOSClass {
	qosClass := pod.Status.QOSClass
	if len(qosClass) <= 0 {
		qosClass = qos.GetPodQOS(pod)
	}
	return qosClass
}

func GetPodBEMilliCPURequest(pod *corev1.Pod) int64 {
	podCPUMilliReq := int64(0)
	// TODO: count init containers and pod overhead
	for _, container := range pod.Spec.Containers {
		containerCPUMilliReq := GetContainerBEMilliCPURequest(&container)
		if containerCPUMilliReq <= 0 {
			return -1
		}
		podCPUMilliReq += containerCPUMilliReq
	}
	if podCPUMilliReq <= 0 {
		return -1
	}
	return podCPUMilliReq
}

func GetPodBEMilliCPULimit(pod *corev1.Pod) int64 {
	podCPUMilliLimit := int64(0)
	// TODO: count init containers and pod overhead
	for _, container := range pod.Spec.Containers {
		containerCPUMilliLimit := GetContainerBEMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			return -1
		}
		podCPUMilliLimit += containerCPUMilliLimit
	}
	if podCPUMilliLimit <= 0 {
		return -1
	}
	return podCPUMilliLimit
}

func GetPodBEMemoryByteRequestIgnoreUnlimited(pod *corev1.Pod) int64 {
	podMemoryByteRequest := int64(0)
	// TODO: count init containers and pod overhead
	for _, container := range pod.Spec.Containers {
		containerMemByteRequest := GetContainerBEMemoryByteRequest(&container)
		if containerMemByteRequest < 0 {
			// consider request of unlimited container as 0
			continue
		}
		podMemoryByteRequest += containerMemByteRequest
	}
	return podMemoryByteRequest
}

func GetPodBEMemoryByteLimit(pod *corev1.Pod) int64 {
	podMemoryByteLimit := int64(0)
	// TODO: count init containers and pod overhead
	for _, container := range pod.Spec.Containers {
		containerMemByteLimit := GetContainerBEMemoryByteLimit(&container)
		if containerMemByteLimit <= 0 {
			return -1
		}
		podMemoryByteLimit += containerMemByteLimit
	}
	if podMemoryByteLimit <= 0 {
		return -1
	}
	return podMemoryByteLimit
}

func GetPodCurCPUShare(podParentDir string) (int64, error) {
	cgroupPath := GetPodCgroupCPUSharePath(podParentDir)
	rawContent, err := ioutil.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetPodCurCFSPeriod(podParentDir string) (int64, error) {
	cgroupPath := GetPodCgroupCFSPeriodPath(podParentDir)
	rawContent, err := ioutil.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetPodCurCFSQuota(podParentDir string) (int64, error) {
	cgroupPath := GetPodCgroupCFSQuotaPath(podParentDir)
	rawContent, err := ioutil.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetPodCurMemLimitBytes(podParentDir string) (int64, error) {
	cgroupPath := GetPodCgroupMemLimitPath(podParentDir)
	rawContent, err := ioutil.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

// @return like kubepods.slice/kubepods-burstable.slice/
func GetPodQoSRelativePath(qosClass corev1.PodQOSClass) string {
	return path.Join(
		system.CgroupPathFormatter.ParentDir,
		system.CgroupPathFormatter.QOSDirFn(qosClass),
	)
}

// @return 7712555c_ce62_454a_9e18_9ff0217b8941 from kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice
func ParsePodID(basename string) (string, error) {
	return system.CgroupPathFormatter.PodIDParser(basename)
}

// @return 7712555c_ce62_454a_9e18_9ff0217b8941 from docker-7712555c_ce62_454a_9e18_9ff0217b8941.scope
func ParseContainerID(basename string) (string, error) {
	return system.CgroupPathFormatter.ContainerIDParser(basename)
}

func GetPodKey(pod *corev1.Pod) string {
	return fmt.Sprintf("%v/%v", pod.GetNamespace(), pod.GetName())
}

func GetPodMetricKey(podMetric *slov1alpha1.PodMetricInfo) string {
	return fmt.Sprintf("%v/%v", podMetric.Namespace, podMetric.Name)
}

func GetPodRequest(pod *corev1.Pod, resourceNames ...corev1.ResourceName) corev1.ResourceList {
	result := corev1.ResourceList{}
	for _, container := range pod.Spec.Containers {
		result = quotav1.Add(result, container.Resources.Requests)
	}
	// take max_resource(sum_pod, any_init_container)
	for _, container := range pod.Spec.InitContainers {
		result = quotav1.Max(result, container.Resources.Requests)
	}
	// add pod overhead if it exists
	if pod.Spec.Overhead != nil {
		result = quotav1.Add(result, pod.Spec.Overhead)
	}
	if len(resourceNames) > 0 {
		result = quotav1.Mask(result, resourceNames)
	}
	return result
}
