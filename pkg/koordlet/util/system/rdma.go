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

package system

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

const (
	SysInfinibandDir = "/sys/class/infiniband"
	RDMAStateActive  = "ACTIVE"
)

var (
	rdmaRegex = regexp.MustCompile(`^mlx\d+_(\d+)$`)
)

// if ibdev is mlx5_1, return 1
func GetRDMAMinor(rdmaDevice string) (int32, error) {
	matches := rdmaRegex.FindStringSubmatch(rdmaDevice)
	if len(matches) != 2 {
		return -1, fmt.Errorf("rdma device %s format is invalid", rdmaDevice)
	}
	minorID, err := strconv.Atoi(matches[1])
	if err != nil {
		return -1, fmt.Errorf("rdma device %s minorID parse error: %w", rdmaDevice, err)
	}
	return int32(minorID), nil
}

// cat /sys/class/infiniband/mlx5_1/ports/1/state
func IsRDMADeviceHealthy(rdmaResource string) bool {
	portsPath := filepath.Join(SysInfinibandDir, rdmaResource, "ports")
	ports, err := os.ReadDir(portsPath)
	if err != nil || len(ports) == 0 {
		klog.Errorf("IsRDMADeviceHealthy(): read rdma device ports dir %s error, %v", portsPath, err)
		return false
	}
	for _, port := range ports {
		stateFile := filepath.Join(portsPath, port.Name(), "state")
		// read info from state file
		rawState, err := os.ReadFile(stateFile)
		if err != nil {
			klog.Errorf("IsRDMADeviceHealthy(): read rdma device state file %s error, %v", stateFile, err)
			return false
		}
		state := string(rawState)
		if !strings.Contains(state, RDMAStateActive) {
			return false
		}
	}
	return true
}
