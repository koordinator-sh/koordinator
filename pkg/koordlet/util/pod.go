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
	"path/filepath"

	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

// ParsePodID parse pod ID from the pod base path.
// e.g. 7712555c_ce62_454a_9e18_9ff0217b8941 from kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice
func ParsePodID(basename string) (string, error) {
	return system.CgroupPathFormatter.PodIDParser(basename)
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

// GetPodSandboxContainerID lists all dirs of pod cgroup(cpuset), exclude containers' dir, get sandbox hash id from the
// remaining dir.
// e.g. return "containerd://91cf0413ee0e6745335e9043b261a829ce07d28a5a66b5ec39b06811ef75a1ff"
func GetPodSandboxContainerID(pod *corev1.Pod) (string, error) {
	cpuSetCgroupRootDir := system.GetRootCgroupSubfsDir(system.CgroupCPUSetDir)
	podCgroupDir := GetPodCgroupParentDir(pod)
	podCPUSetCgroupRootDir := filepath.Join(cpuSetCgroupRootDir, podCgroupDir)

	if len(pod.Status.ContainerStatuses) <= 0 {
		// container not created, skip until container is created because container runtime is unknown
		return "", nil
	}

	// get runtime type and dir names of known containers
	containerSubDirNames := make(map[string]struct{}, len(pod.Status.ContainerStatuses))
	containerRuntime := system.RuntimeTypeUnknown
	for _, containerStat := range pod.Status.ContainerStatuses {
		runtimeType, containerDirName, err := system.CgroupPathFormatter.ContainerDirFn(containerStat.ContainerID)
		if err != nil {
			return "", err
		}
		containerSubDirNames[containerDirName] = struct{}{}
		containerRuntime = runtimeType
	}

	sandboxCandidates := make([]string, 0)
	containerDirs, err := os.ReadDir(podCPUSetCgroupRootDir)
	if err != nil {
		return "", err
	}
	for _, containerDir := range containerDirs {
		if !containerDir.IsDir() {
			continue
		}
		if _, exist := containerSubDirNames[containerDir.Name()]; !exist {
			sandboxCandidates = append(sandboxCandidates, containerDir.Name())
		}
	}

	if len(sandboxCandidates) != 1 {
		return "", fmt.Errorf("candidates of sandbox id is %v, some container is not ready, detail %v",
			len(sandboxCandidates), sandboxCandidates)
	}

	containerHashID, err := system.CgroupPathFormatter.ContainerIDParser(sandboxCandidates[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s", containerRuntime, containerHashID), nil
}
