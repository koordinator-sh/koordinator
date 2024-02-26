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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

const (
	PodCgroupPathRelativeDepth       = 1
	ContainerCgroupPathRelativeDepth = 2
)

// GetRootCgroupCPUSetDir gets the cpuset parent directory of the specified podQos' root cgroup
// @output /sys/fs/cgroup/cpuset/kubepods.slice/kubepods-besteffort.slice
func GetRootCgroupCPUSetDir(qosClass corev1.PodQOSClass) string {
	rootCgroupParentDir := GetPodQoSRelativePath(qosClass)
	cpuSet, _ := system.GetCgroupResource(system.CPUSetCPUSName)
	return filepath.Dir(cpuSet.Path(rootCgroupParentDir))
}

// GetBECgroupCurCPUSet gets the current cpuset of besteffort podQoS' cgroup.
func GetBECgroupCurCPUSet() ([]int32, error) {
	targetCgroupDir := GetPodQoSRelativePath(corev1.PodQOSBestEffort)
	containerPaths, err := GetBECPUSetPathsByTargetDepth(ContainerCgroupPathRelativeDepth)
	if err != nil {
		return nil, err
	}
	if len(containerPaths) != 0 {
		targetCgroupDir = containerPaths[0]
	}

	cpus, err := resourceexecutor.NewCgroupReader().ReadCPUSet(targetCgroupDir)
	if err != nil {
		return nil, err
	}
	return cpuset.ParseCPUSet(cpus), nil
}

// GetBECPUSetPathsByMaxDepth gets all the be cpuset groups' paths recursively from upper to lower
func GetBECPUSetPathsByMaxDepth(relativeDepth int) ([]string, error) {
	// walk from root path to lower nodes
	rootCgroupPath := GetRootCgroupCPUSetDir(corev1.PodQOSBestEffort)
	rootCPUSetSubfsPath := system.GetRootCgroupSubfsDir(system.CgroupCPUSetDir)
	_, err := os.Stat(rootCgroupPath)
	if err != nil {
		// make sure the rootCgroupPath is available
		return nil, err
	}
	klog.V(6).Infof("get be rootCgroupPath: %v", rootCgroupPath)

	absDepth := strings.Count(rootCgroupPath, string(os.PathSeparator)) + relativeDepth
	var paths []string
	err = filepath.Walk(rootCgroupPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && strings.Count(path, string(os.PathSeparator)) <= absDepth {
			// get the path of parentDir
			parentDir, err1 := filepath.Rel(rootCPUSetSubfsPath, path)
			if err1 != nil {
				return err1
			}
			paths = append(paths, parentDir)
		}
		return nil
	})
	return paths, err
}

func GetCgroupPathsByTargetDepth(qos corev1.PodQOSClass, resourceType system.ResourceType, relativeDepth int) ([]string, error) {
	rootCgroupParentDir := GetPodQoSRelativePath(qos)
	r, err := system.GetCgroupResource(resourceType)
	if err != nil {
		return nil, fmt.Errorf("get resource type failed, err: %w", err)
	}
	cgroupResource := r.(*system.CgroupResource)
	rootCgroupPath := filepath.Dir(r.Path(rootCgroupParentDir))
	rootSubfsPath := system.GetRootCgroupSubfsDir(cgroupResource.Subfs)
	_, err = os.Stat(rootCgroupPath)
	if err != nil {
		// make sure the rootCgroupPath is available
		return nil, err
	}
	klog.V(6).Infof("get rootCgroupPath, qos %s, resource %s, path: %s", qos, resourceType, rootCgroupPath)

	absDepth := strings.Count(rootCgroupPath, string(os.PathSeparator)) + relativeDepth
	var containerPaths []string
	err = filepath.WalkDir(rootCgroupPath, func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && strings.Count(path, string(os.PathSeparator)) == absDepth {
			// get the path of parentDir
			parentDir, err1 := filepath.Rel(rootSubfsPath, path)
			if err1 != nil {
				return err1
			}
			containerPaths = append(containerPaths, parentDir)
		}
		return nil
	})
	return containerPaths, err
}

// GetBECPUSetPathsByTargetDepth only gets the be containers' cpuset groups' paths
func GetBECPUSetPathsByTargetDepth(relativeDepth int) ([]string, error) {
	return GetCgroupPathsByTargetDepth(corev1.PodQOSBestEffort, system.CPUSetCPUSName, relativeDepth)
}

// GetCgroupRootBlkIOAbsoluteDir gets the root blkio directory
// @output /sys/fs/cgroup/blkio
func GetCgroupRootBlkIOAbsoluteDir() string {
	return filepath.Join(system.Conf.CgroupRootDir, system.CgroupBlkioDir)
}

// GetPodCgroupBlkIOAbsoluteDir gets the blkio parent directory of the specified podQos' root cgroup
// @output /sys/fs/cgroup/blkio/kubepods.slice/kubepods-besteffort.slice
func GetPodCgroupBlkIOAbsoluteDir(qosClass corev1.PodQOSClass) string {
	podCgroupParentDir := GetPodQoSRelativePath(qosClass)
	return filepath.Join(GetCgroupRootBlkIOAbsoluteDir(), podCgroupParentDir)
}
