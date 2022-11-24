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
	"strconv"
	"strings"

	"k8s.io/klog/v2"
	utilsysctl "k8s.io/kubernetes/pkg/util/sysctl"
)

const (
	ProcStatName = "stat"
	SysctlSubDir = "sys"

	KernelSchedGroupIdentityEnable = "kernel/sched_group_identity_enabled"
)

type SystemFile struct {
	File      string
	Validator RangeValidator
}

var (
	ProcStatFile SystemFile
)

func init() {
	initFilePath()
}

func initFilePath() {
	ProcStatFile = SystemFile{File: filepath.Join(Conf.ProcRootDir, ProcStatName)}
}

func GetProcSysFilePath(file string) string {
	return filepath.Join(Conf.ProcRootDir, SysctlSubDir, file)
}

var _ utilsysctl.Interface = &ProcSysctl{}

// ProcSysctl implements Interface by reading and writing files under /proc/sys
type ProcSysctl struct{}

func NewProcSysctl() utilsysctl.Interface {
	return &ProcSysctl{}
}

func (*ProcSysctl) GetSysctl(sysctl string) (int, error) {
	data, err := os.ReadFile(GetProcSysFilePath(sysctl))
	if err != nil {
		return -1, err
	}
	val, err := strconv.Atoi(strings.Trim(string(data), " \n"))
	if err != nil {
		return -1, err
	}
	return val, nil
}

// SetSysctl modifies the specified sysctl flag to the new value
func (*ProcSysctl) SetSysctl(sysctl string, newVal int) error {
	return os.WriteFile(GetProcSysFilePath(sysctl), []byte(strconv.Itoa(newVal)), 0640)
}

func SetSchedGroupIdentity(enable bool) error {
	s := NewProcSysctl()
	cur, err := s.GetSysctl(KernelSchedGroupIdentityEnable)
	if err != nil {
		return fmt.Errorf("cannot get sysctl group identity, err: %v", err)
	}
	v := 0 // 0: disabled; 1: enabled
	if enable {
		v = 1
	}
	if cur == v {
		klog.V(6).Infof("SetSchedGroupIdentity skips since current sysctl config is already %v", enable)
		return nil
	}

	err = s.SetSysctl(KernelSchedGroupIdentityEnable, v)
	if err != nil {
		return fmt.Errorf("cannot set sysctl group identity, err: %v", err)
	}
	klog.V(4).Infof("SetSchedGroupIdentity set sysctl config successfully, value %v", v)
	return nil
}
