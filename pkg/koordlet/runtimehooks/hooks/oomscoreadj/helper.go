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

package oomscoreadj

import (
	"encoding/json"
	"fmt"
	"strconv"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

// getOOMScoreAdjForContainer returns the target oom_score_adj for the given container.
// It first checks AnnotationOOMScoreAdjSpec (per-container JSON map), then falls back to
// AnnotationOOMScoreAdj (pod-level default). Returns (nil, nil) if neither is set.
func getOOMScoreAdjForContainer(podAnnotations map[string]string, containerName string) (*int64, error) {
	if podAnnotations == nil {
		return nil, nil
	}

	// Try per-container spec first.
	if spec, ok := podAnnotations[extension.AnnotationOOMScoreAdjSpec]; ok && spec != "" {
		containerSpec := map[string]int64{}
		if err := json.Unmarshal([]byte(spec), &containerSpec); err != nil {
			return nil, fmt.Errorf("failed to parse annotation %s as JSON: %w", extension.AnnotationOOMScoreAdjSpec, err)
		}
		if val, exists := containerSpec[containerName]; exists {
			return &val, nil
		}
	}

	// Fall back to pod-level default.
	if defaultVal, ok := podAnnotations[extension.AnnotationOOMScoreAdj]; ok && defaultVal != "" {
		val, err := strconv.ParseInt(defaultVal, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse annotation %s as int64: %w", extension.AnnotationOOMScoreAdj, err)
		}
		return &val, nil
	}

	return nil, nil
}

// getContainerPIDs reads the PIDs from the container's cgroup directory.
func getContainerPIDs(reader resourceexecutor.CgroupReader, cgroupParent string) ([]uint32, error) {
	pids, err := reader.ReadCPUProcs(cgroupParent)
	if err != nil && resourceexecutor.IsCgroupDirErr(err) {
		klog.V(5).Infof("aborted to get PIDs for container dir %s, err: %s", cgroupParent, err)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get container PIDs failed, err: %w", err)
	}
	return pids, nil
}

// setOOMScoreAdjForPIDs writes the target oom_score_adj to each PID via the given interface,
// skipping PIDs that already match. Returns (updated, skipped).
func setOOMScoreAdjForPIDs(ome sysutil.OOMScoreAdjInterface, pids []uint32, target int64) (int, int) {
	var updated, skipped int
	for _, pid := range pids {
		current, err := ome.Get(pid)
		if err != nil {
			klog.V(5).Infof("failed to read oom_score_adj for pid %d, skipping: %s", pid, err)
			skipped++
			continue
		}
		if current == target {
			continue
		}
		if err := ome.Set(pid, target); err != nil {
			klog.V(4).Infof("failed to write oom_score_adj=%d for pid %d: %s", target, pid, err)
			skipped++
			continue
		}
		updated++
	}
	return updated, skipped
}
