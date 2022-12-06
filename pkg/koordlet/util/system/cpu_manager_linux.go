//go:build linux
// +build linux

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
)

func GetKubeletCommandline(port int) ([]string, error) {
	kubeletPid, err := KubeletPortToPid(port)
	if err != nil {
		return nil, err
	}

	kubeletArgs, err := ProcCmdLine(Conf.ProcRootDir, kubeletPid)
	if err != nil || len(kubeletArgs) <= 1 {
		return nil, fmt.Errorf("failed to get kubelet's args: %v", err)
	}
	return kubeletArgs, nil
}
