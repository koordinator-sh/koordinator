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
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/errors"
	utilsysctl "k8s.io/component-helpers/node/util/sysctl"
	"k8s.io/klog/v2"
)

const (
	SysctlSubDir          = "sys"
	KernelCmdlineFileName = "cmdline"
	HugepageDir           = "hugepages"
	nrPath                = "nr_hugepages"

	KernelSchedGroupIdentityEnable = SysKernelRelativePath + SchedGroupIdentityEnabledFileName
	KernelSchedCore                = SysKernelRelativePath + SchedCoreFileName

	SysNUMASubDir   = "bus/node/devices"
	SysPCIDeviceDir = "bus/pci/devices"

	SysCPUSMTActiveSubPath       = "devices/system/cpu/smt/active"
	SysIntelPStateNoTurboSubPath = "devices/system/cpu/intel_pstate/no_turbo"
)

var (
	// Jiffies is the duration unit of CPU stats. Normally, it is 10ms.
	Jiffies = float64(10 * time.Millisecond)
)

// initJiffies use command "getconf CLK_TCK" to fetch the clock tick on current host,
// if the command doesn't exist, uses the default value 10ms for jiffies
func initJiffies() error {
	getconf, err := exec.LookPath("getconf")
	if err != nil {
		return err
	}
	cmd := exec.Command(getconf, "CLK_TCK")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err = cmd.Run(); err != nil {
		return err
	}
	ticks, err := strconv.ParseFloat(strings.TrimSpace(out.String()), 64)
	if err != nil {
		return err
	}
	Jiffies = float64(time.Second / time.Duration(ticks))
	return nil
}

func GetPeriodTicks(start, end time.Time) float64 {
	return float64(end.Sub(start)) / Jiffies
}

func GetSysRootDir() string {
	return Conf.SysRootDir
}

func GetSysNUMADir() string {
	return filepath.Join(Conf.SysRootDir, SysNUMASubDir)
}

func GetNUMAMemInfoPath(numaNodeSubDir string) string {
	return filepath.Join(Conf.SysRootDir, SysNUMASubDir, numaNodeSubDir, ProcMemInfoName)
}

func GetNUMAHugepagesDir(numaNodeSubDir string) string {
	return filepath.Join(Conf.SysRootDir, SysNUMASubDir, numaNodeSubDir, HugepageDir)
}

func GetNUMAHugepagesNrPath(numaNodeSubDir string, page string) string {
	return filepath.Join(Conf.SysRootDir, SysNUMASubDir, numaNodeSubDir, HugepageDir, page, nrPath)
}

func GetCPUInfoPath() string {
	return filepath.Join(Conf.ProcRootDir, ProcCPUInfoName)
}

func GetSysCPUSMTActivePath() string {
	return filepath.Join(Conf.SysRootDir, SysCPUSMTActiveSubPath)
}

func GetSysIntelPStateNoTurboPath() string {
	return filepath.Join(Conf.SysRootDir, SysIntelPStateNoTurboSubPath)
}

func GetProcSysFilePath(file string) string {
	return filepath.Join(Conf.ProcRootDir, SysctlSubDir, file)
}

func GetPCIDeviceDir() string { return filepath.Join(Conf.SysRootDir, SysPCIDeviceDir) }

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

func IsGroupIdentitySysctlSupported() bool {
	return FileExists(GetProcSysFilePath(KernelSchedGroupIdentityEnable))
}

func GetSchedGroupIdentity() (bool, error) {
	s := NewProcSysctl()
	// 0: disabled; 1: enabled
	cur, err := s.GetSysctl(KernelSchedGroupIdentityEnable)
	if err != nil {
		return false, fmt.Errorf("cannot get sysctl group identity, err: %w", err)
	}
	return cur == 1, nil
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
		return fmt.Errorf("cannot set sysctl group identity, err: %w", err)
	}
	klog.V(4).Infof("SetSchedGroupIdentity set sysctl config successfully, value %v", v)
	return nil
}

func GetSchedCore() (bool, error) {
	s := NewProcSysctl()
	// 0: disabled; 1: enabled
	cur, err := s.GetSysctl(KernelSchedCore)
	if err != nil {
		return false, fmt.Errorf("cannot get sysctl sched core, err: %w", err)
	}
	return cur == 1, nil
}

func SetSchedCore(enable bool) error {
	s := NewProcSysctl()
	cur, err := s.GetSysctl(KernelSchedCore)
	if err != nil {
		return fmt.Errorf("cannot get sysctl sched core, err: %w", err)
	}
	v := 0 // 0: disabled; 1: enabled
	if enable {
		v = 1
	}
	if cur == v {
		klog.V(6).Infof("SetSchedCore skips since current sysctl config is already %v", enable)
		return nil
	}

	err = s.SetSysctl(KernelSchedCore, v)
	if err != nil {
		return fmt.Errorf("cannot set sysctl sched core, err: %w", err)
	}
	klog.V(4).Infof("SetSchedCore set sysctl config successfully, value %v", v)
	return nil
}

func GetSchedFeatures() (map[string]bool, error) {
	featurePath := SchedFeatures.Path("")
	content, err := os.ReadFile(featurePath)
	if err != nil {
		klog.V(5).Infof("sched_features is unsupported, path %s, read err: %s", featurePath, err)
		return nil, fmt.Errorf("failed to read sched_features, err: %w", err)
	}

	var errs []error
	schedFeatureMap := map[string]bool{}
	features := strings.Fields(string(content))
	for _, feature := range features {
		if strings.HasPrefix(feature, "NO_") {
			featureName := strings.TrimPrefix(feature, "NO_")
			if v, ok := schedFeatureMap[featureName]; ok && v {
				errs = append(errs, fmt.Errorf("failed to read conflict sched_features, feature %s", featureName))
				continue
			}
			schedFeatureMap[featureName] = false
		} else {
			if v, ok := schedFeatureMap[feature]; ok && !v {
				errs = append(errs, fmt.Errorf("failed to read conflict sched_features, feature %s", feature))
				continue
			}
			schedFeatureMap[feature] = true
		}
	}

	return schedFeatureMap, errors.NewAggregate(errs)
}

func SetSchedFeatures(featureMap map[string]bool, valueMap map[string]bool) error {
	if featureMap == nil && valueMap == nil {
		return nil
	}
	if featureMap == nil {
		return fmt.Errorf("cannot set to nil featureMap")
	}

	var errs []error
	featurePath := SchedFeatures.Path("")
	for featureName, value := range valueMap {
		if _, ok := featureMap[featureName]; ok && value == featureMap[featureName] {
			klog.V(6).Infof("skip to set unchanged sched_feature, feature %s, value %v", featureName, value)
			continue
		}
		if value { // write XXX to sched_features
			err := os.WriteFile(featurePath, []byte(fmt.Sprintf("%s\n", featureName)), 0666)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to write sched_features, feature %s, value %v, err: %w", featureName, value, err))
				continue
			}
		} else { // write NO_XXX to sched_features
			err := os.WriteFile(featurePath, []byte(fmt.Sprintf("NO_%s\n", featureName)), 0666)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to write sched_features, feature %s, value %v, err: %w", featureName, value, err))
				continue
			}
		}
	}
	return errors.NewAggregate(errs)
}
