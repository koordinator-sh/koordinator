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

package rdma

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"k8s.io/klog"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/devices/helper"
)

const (
	IBDevDir     = "/dev/infiniband"
	RDMACM       = "/dev/infiniband/rdma_cm"
	RdmaClassDir = "/sys/class/infiniband"
)

func GetUVerbsViaPciAdd(pciAddress string) (string, error) {
	pciDir := filepath.Join(helper.SysBusPci, pciAddress, "infiniband_verbs")
	files, err := ioutil.ReadDir(pciDir)
	if err != nil || len(files) == 0 {
		klog.Errorf("fail read pciDir %v", err)
		return "", fmt.Errorf("failed to get uverbs: %s", err.Error())
	}
	return filepath.Join(IBDevDir, files[0].Name()), nil
}

func IsNodeHasRDMADevice() bool {
	entries, err := os.ReadDir(RdmaClassDir)
	if err != nil {
		return false
	}
	return len(entries) != 0
}
