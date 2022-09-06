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
	"path"
	"path/filepath"

	"github.com/spf13/pflag"
	cliflag "k8s.io/component-base/cli/flag"
)

// return cpu policy, cpu state file path and cpu manager opt
func GuessCPUManagerOptFromKubeletPort(port int) (string, string, map[string]string, error) {
	kubeletPid, err := KubeletPortToPid(port)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to find kubelet's pid, kubelet may stop: %v", err)
	}

	kubeletArgs, err := ProcCmdLine(Conf.ProcRootDir, kubeletPid)
	if err != nil || len(kubeletArgs) <= 1 {
		return "", "", nil, fmt.Errorf("failed to get kubelet's args: %v", err)
	}
	var argsRootDir string
	argsCpuManagerOpt := map[string]string{}
	var argsCpuPolicy string
	fs := pflag.NewFlagSet("GuessTest", pflag.ContinueOnError)
	fs.ParseErrorsWhitelist.UnknownFlags = true
	fs.StringVar(&argsRootDir, "root-dir", "/var/lib/kubelet", "")
	fs.StringVar(&argsCpuPolicy, "cpu-manager-policy", "", "")
	fs.Var(cliflag.NewMapStringStringNoSplit(&argsCpuManagerOpt), "cpu-manager-policy-options", "")
	if err := fs.Parse(kubeletArgs[1:]); err != nil {
		return "", "", nil, fmt.Errorf("failed to parse kubelet's args, kubelet version may not support: %v", err)
	}
	return argsCpuPolicy, path.Join(argsRootDir, "cpu_manager_state"), argsCpuManagerOpt, nil
}

// return kubelet config file path
func GuessConfigFilePathFromKubeletPort(port int) (string, error) {
	kubeletPid, err := KubeletPortToPid(port)
	if err != nil {
		return "", fmt.Errorf("failed to find kubelet's pid, kubelet may stop: %v", err)
	}

	kubeletArgs, err := ProcCmdLine(Conf.ProcRootDir, kubeletPid)
	if err != nil || len(kubeletArgs) <= 1 {
		return "", fmt.Errorf("failed to get kubelet's args: %v", err)
	}
	var argsFilePath string
	fs := pflag.NewFlagSet("GuessTest", pflag.ContinueOnError)
	fs.ParseErrorsWhitelist.UnknownFlags = true
	fs.StringVar(&argsFilePath, "config", argsFilePath, "")
	if err := fs.Parse(kubeletArgs[1:]); err != nil {
		return "", fmt.Errorf("failed to parse kubelet's args, kubelet version may not support: %v", err)
	}
	if argsFilePath == "" {
		return "", nil
	}
	if FileExists(argsFilePath) {
		return argsFilePath, nil
	}
	wd, err := WorkingDirOf(kubeletPid)
	if err != nil {
		return "", err
	}
	absPath := filepath.Join(wd, argsFilePath)
	if FileExists(absPath) {
		return absPath, nil
	}
	return "", fmt.Errorf("failed to get kubelet config file")
}
