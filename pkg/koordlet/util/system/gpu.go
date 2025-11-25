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
	"os"
	"regexp"
	"strings"

	"k8s.io/klog/v2"
)

const (
	NVIDADriverDir = "/sys/bus/pci/drivers/nvidia"
)

var (
	adressRegex = regexp.MustCompile(`^((1?[0-9a-f]{0,4}):)?([0-9a-f]{2}):([0-9a-f]{2})\.([0-9a-f]{1})$`)
)

func GetGPUDevicePciBusIDs() []string {
	var pciBusIds []string
	entries, err := os.ReadDir(NVIDADriverDir)
	if err != nil {
		klog.Errorf("GetGPUDevicePciBusIDs: read nvidia driver dir %s error, %v", NVIDADriverDir, err)
		return pciBusIds
	}

	for _, entry := range entries {
		fileName := strings.ToLower(entry.Name())
		matches := adressRegex.FindStringSubmatch(fileName)
		if len(matches) == 6 {
			pciBusIds = append(pciBusIds, matches[0])
		}
	}

	return pciBusIds
}
