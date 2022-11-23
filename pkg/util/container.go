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
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func GetEmptyContainerExtendedResources() *apiext.ExtendedResourceContainerSpec {
	return &apiext.ExtendedResourceContainerSpec{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
}

func GetContainerExtendedResources(container *corev1.Container) *apiext.ExtendedResourceContainerSpec {
	return GetContainerTargetExtendedResources(container, ExtendedResourceNames...)
}

// GetContainerTargetExtendedResources gets the resource requirements of a container with given extended resources.
// It returns nil if container is nil or specifies no extended resource.
func GetContainerTargetExtendedResources(container *corev1.Container, resourceNames ...corev1.ResourceName) *apiext.ExtendedResourceContainerSpec {
	if container == nil {
		return nil
	}

	r := GetEmptyContainerExtendedResources()

	for _, name := range resourceNames {
		// assert container.Resources.Requests != nil && container.Resources.Limits != nil
		q, ok := container.Resources.Requests[name]
		if ok {
			r.Requests[name] = q
		}
		q, ok = container.Resources.Limits[name]
		if ok {
			r.Limits[name] = q
		}
	}

	if len(r.Requests) <= 0 && len(r.Limits) <= 0 { // no requirement of specified extended resources
		return nil
	}

	return r
}

// @parentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @return kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/****.scope
func GetContainerCgroupPathWithKube(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	return GetContainerCgroupPathWithKubeByID(podParentDir, c.ContainerID)
}

// @parentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @return kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/****.scope
func GetContainerCgroupPathWithKubeByID(podParentDir string, containerID string) (string, error) {
	containerDir, err := system.CgroupPathFormatter.ContainerDirFn(containerID)
	if err != nil {
		return "", err
	}
	return path.Join(
		GetPodCgroupDirWithKube(podParentDir),
		containerDir,
	), nil
}

// @parentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @return /sys/fs/cgroup/cpu/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cgroup.procs
func GetContainerCgroupCPUProcsPath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return system.GetCgroupFilePath(containerPath, system.CPUProcs), nil
}

func GetContainerCgroupPerfPath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return path.Join(system.Conf.CgroupRootDir, "perf_event/", containerPath), nil
}

func GetContainerCgroupCPUAcctUsagePath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return system.GetCgroupFilePath(containerPath, system.CpuacctUsage), nil
}

func GetContainerCgroupMemStatPath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return system.GetCgroupFilePath(containerPath, system.MemStat), nil
}

func GetContainerBaseCFSQuota(container *corev1.Container) int64 {
	cpuMilliLimit := GetContainerMilliCPULimit(container)
	if cpuMilliLimit <= 0 {
		return -1
	} else {
		return cpuMilliLimit * system.CFSBasePeriodValue / 1000
	}
}

func GetContainerCgroupCPUStatPath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return system.GetCgroupFilePath(containerPath, system.CPUStat), nil
}

func GetContainerCgroupMemLimitPath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return system.GetCgroupFilePath(containerPath, system.MemoryLimit), nil
}

func GetContainerMilliCPULimit(c *corev1.Container) int64 {
	if cpuLimit, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
		return cpuLimit.MilliValue()
	}
	return -1
}

func GetContainerMemoryByteLimit(c *corev1.Container) int64 {
	if memLimit, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
		return memLimit.Value()
	}
	return -1
}

func GetContainerCgroupCPUSharePath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return system.GetCgroupFilePath(containerPath, system.CPUShares), nil
}

func GetContainerCgroupCFSPeriodPath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return system.GetCgroupFilePath(containerPath, system.CPUCFSPeriod), nil
}

func GetContainerCgroupCFSQuotaPath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return system.GetCgroupFilePath(containerPath, system.CPUCFSQuota), nil
}

func GetContainerCurTasksPath(podParentDir string, c *corev1.ContainerStatus) (string, error) {
	containerPath, err := GetContainerCgroupPathWithKube(podParentDir, c)
	if err != nil {
		return "", err
	}
	return system.GetCgroupFilePath(containerPath, system.CPUTask), nil
}

func GetContainerCurCPUShare(podParentDir string, c *corev1.ContainerStatus) (int64, error) {
	cgroupPath, err := GetContainerCgroupCPUSharePath(podParentDir, c)
	if err != nil {
		return 0, err
	}
	rawContent, err := os.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetContainerCurCFSPeriod(podParentDir string, c *corev1.ContainerStatus) (int64, error) {
	cgroupPath, err := GetContainerCgroupCFSPeriodPath(podParentDir, c)
	if err != nil {
		return 0, err
	}
	rawContent, err := os.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetContainerCurCFSQuota(podParentDir string, c *corev1.ContainerStatus) (int64, error) {
	cgroupPath, err := GetContainerCgroupCFSQuotaPath(podParentDir, c)
	if err != nil {
		return 0, err
	}
	rawContent, err := os.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetContainerCurMemLimitBytes(podParentDir string, c *corev1.ContainerStatus) (int64, error) {
	cgroupPath, err := GetContainerCgroupMemLimitPath(podParentDir, c)
	if err != nil {
		return 0, err
	}
	rawContent, err := os.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetContainerCurTasks(podParentDir string, c *corev1.ContainerStatus) ([]int, error) {
	cgroupPath, err := GetContainerCurTasksPath(podParentDir, c)
	if err != nil {
		return nil, err
	}
	return system.GetCgroupCurTasks(cgroupPath)
}

func GetPIDsInPod(podParentDir string, cs []corev1.ContainerStatus) ([]uint32, error) {
	pids := make([]uint32, 0)
	for i := range cs {
		p, err := GetPIDsInContainer(podParentDir, &cs[i])
		if err != nil {
			return nil, err
		}
		pids = append(pids, p...)
	}
	return pids, nil
}

func GetPIDsInContainer(podParentDir string, c *corev1.ContainerStatus) ([]uint32, error) {
	cgroupPath, err := GetContainerCgroupCPUProcsPath(podParentDir, c)
	if err != nil {
		return nil, err
	}
	rawContent, err := os.ReadFile(cgroupPath)
	if err != nil {
		return nil, err
	}
	pidStrs := strings.Fields(strings.TrimSpace(string(rawContent)))
	pids := make([]uint32, len(pidStrs))

	for i := 0; i < len(pids); i++ {
		p, err := strconv.ParseUint(pidStrs[i], 10, 32)
		if err != nil {
			return nil, err
		}
		pids[i] = uint32(p)
	}
	return pids, nil
}

func FindContainerIdAndStatusByName(status *corev1.PodStatus, name string) (string, *corev1.ContainerStatus, error) {
	allStatuses := status.InitContainerStatuses
	allStatuses = append(allStatuses, status.ContainerStatuses...)
	for _, container := range allStatuses {
		if container.Name == name && container.ContainerID != "" {
			_, cID, err := ParseContainerId(container.ContainerID)
			if err != nil {
				return "", nil, err
			}
			return cID, &container, nil
		}
	}
	return "", nil, fmt.Errorf("unable to find ID for container with name %v in pod status (it may not be running)", name)
}

func ParseContainerId(data string) (cType, cID string, err error) {
	// Trim the quotes and split the type and ID.
	parts := strings.Split(strings.Trim(data, "\""), "://")
	if len(parts) != 2 {
		err = fmt.Errorf("invalid container ID: %q", data)
		return
	}
	cType, cID = parts[0], parts[1]
	return
}

// WriteCgroupCPUSet writes the cgroup cpuset file according to the specified cgroup dir
func WriteCgroupCPUSet(cgroupFileDir, cpusetStr string) error {
	return os.WriteFile(filepath.Join(cgroupFileDir, system.CPUSFileName), []byte(cpusetStr), 0644)
}
