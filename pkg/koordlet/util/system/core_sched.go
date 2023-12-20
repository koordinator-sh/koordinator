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
	"strings"

	"k8s.io/klog/v2"
)

const (
	// SchedFeatureCoreSched is the feature name of the core scheduling in `/sys/kernel/debug/sched_features`.
	SchedFeatureCoreSched = "CORE_SCHED"
	// SchedFeatureNoCoreSched is the feature name that the core scheduling supported but disabled.
	SchedFeatureNoCoreSched = "NO_CORE_SCHED"
)

// CoreSchedScopeType defines the type of the PID type operated in core sched.
type CoreSchedScopeType uint

const (
	// CoreSchedScopeThread means the PID type operated in core sched is a thread.
	CoreSchedScopeThread CoreSchedScopeType = iota
	// CoreSchedScopeThreadGroup means the PID type operated in core sched is a thread group.
	CoreSchedScopeThreadGroup
	// CoreSchedScopeProcessGroup means the PID type operated in core sched is a process group.
	CoreSchedScopeProcessGroup
)

// CoreSchedInterface defines the basic operations of the Linux Core Scheduling.
// https://docs.kernel.org/admin-guide/hw-vuln/core-scheduling.html
type CoreSchedInterface interface {
	// Get gets core sched cookie of pid.
	Get(pidType CoreSchedScopeType, pid uint32) (uint64, error)
	// Create creates a new unique cookie to pid.
	Create(pidType CoreSchedScopeType, pid uint32) error
	// ShareTo push core sched cookie (of the current task) to pid.
	ShareTo(pidType CoreSchedScopeType, pid uint32) error
	// ShareFrom pull core sched cookie from pid (to the current task).
	ShareFrom(pidType CoreSchedScopeType, pid uint32) error
}

// CoreSchedExtendedInterface defines the operations of the Linux Core Scheduling including extended OPs.
type CoreSchedExtendedInterface interface {
	CoreSchedInterface
	// Clear clears core sched cookie to the default cookie 0, and returns the list of failed pids.
	Clear(pidType CoreSchedScopeType, pids ...uint32) ([]uint32, error)
	// Assign assigns core sched cookie of the pidFrom onto pidsTo, and returns the list of failed pidTos.
	Assign(pidTypeFrom CoreSchedScopeType, pidFrom uint32, pidTypeTo CoreSchedScopeType, pidsTo ...uint32) ([]uint32, error)
}

// FakeCoreSchedExtended implements the fake CoreSchedExtendedInterface for testing.
type FakeCoreSchedExtended struct {
	PIDToCookie  map[uint32]uint64
	PIDToPGID    map[uint32]uint32
	PIDToTGID    map[uint32]uint32
	PIDToError   map[uint32]bool
	CurPID       uint32
	NextCookieID uint64
}

func NewFakeCoreSchedExtended(pidToCookie map[uint32]uint64, pidToPGID map[uint32]uint32, pidToError map[uint32]bool) CoreSchedExtendedInterface {
	f := &FakeCoreSchedExtended{
		PIDToCookie:  pidToCookie,
		PIDToPGID:    pidToPGID,
		PIDToError:   pidToError,
		CurPID:       1,
		NextCookieID: 1,
	}
	if f.PIDToCookie == nil {
		f.PIDToCookie = map[uint32]uint64{}
	}
	if f.PIDToPGID == nil {
		f.PIDToPGID = map[uint32]uint32{}
	}
	if f.PIDToTGID == nil {
		f.PIDToTGID = f.PIDToPGID
	}
	if f.PIDToError == nil {
		f.PIDToError = map[uint32]bool{}
	}
	for pid, pgid := range pidToPGID {
		f.PIDToPGID[pid] = pgid
	}
	return f
}

func (f *FakeCoreSchedExtended) SetCurPID(pid uint32) {
	f.CurPID = pid
}

func (f *FakeCoreSchedExtended) SetNextCookieID(id uint64) {
	f.NextCookieID = id
}

func (f *FakeCoreSchedExtended) Get(pidType CoreSchedScopeType, pid uint32) (uint64, error) {
	if _, ok := f.PIDToError[pid]; ok {
		return 0, fmt.Errorf("get cookie error")
	}
	if pidType != CoreSchedScopeThread {
		return 0, fmt.Errorf("unsupported pid type %d", pidType)
	}
	if v, ok := f.PIDToCookie[pid]; ok {
		return v, nil
	}
	return 0, nil
}

func (f *FakeCoreSchedExtended) Create(pidType CoreSchedScopeType, pid uint32) error {
	if _, ok := f.PIDToError[pid]; ok {
		return fmt.Errorf("create cookie error")
	}
	f.PIDToCookie[pid] = f.NextCookieID
	if pidType == CoreSchedScopeProcessGroup {
		for cPID, pgid := range f.PIDToPGID {
			if pgid == pid {
				f.PIDToCookie[cPID] = f.NextCookieID
			}
		}
	} else if pidType == CoreSchedScopeThreadGroup {
		for cPID, tgid := range f.PIDToTGID {
			if tgid == pid {
				f.PIDToCookie[cPID] = f.NextCookieID
			}
		}
	}
	f.NextCookieID++
	return nil
}

func (f *FakeCoreSchedExtended) ShareTo(pidType CoreSchedScopeType, pid uint32) error {
	if _, ok := f.PIDToError[pid]; ok {
		return fmt.Errorf("shareTo cookie error")
	}
	curCookieID := f.PIDToCookie[f.CurPID]
	f.PIDToCookie[pid] = curCookieID
	if pidType == CoreSchedScopeProcessGroup {
		for cPID, pgid := range f.PIDToPGID {
			if pgid == pid {
				f.PIDToCookie[cPID] = curCookieID
			}
		}
	} else if pidType == CoreSchedScopeThreadGroup {
		for cPID, tgid := range f.PIDToTGID {
			if tgid == pid {
				f.PIDToCookie[cPID] = curCookieID
			}
		}
	}
	return nil
}

func (f *FakeCoreSchedExtended) ShareFrom(pidType CoreSchedScopeType, pid uint32) error {
	if _, ok := f.PIDToError[pid]; ok {
		return fmt.Errorf("shareFrom cookie error")
	}
	if pidType != CoreSchedScopeThread {
		return fmt.Errorf("unsupported pid type %d", pidType)
	}
	f.PIDToCookie[f.CurPID] = f.PIDToCookie[pid]
	return nil
}

func (f *FakeCoreSchedExtended) Clear(pidType CoreSchedScopeType, pids ...uint32) ([]uint32, error) {
	var failedPIDs []uint32
	for _, pid := range pids {
		if _, ok := f.PIDToError[pid]; ok {
			failedPIDs = append(failedPIDs, pid)
			continue
		}
		f.PIDToCookie[pid] = 0
		if pidType == CoreSchedScopeProcessGroup {
			for cPID, pgid := range f.PIDToPGID {
				if pgid == pid {
					f.PIDToCookie[cPID] = 0
				}
			}
		} else if pidType == CoreSchedScopeThreadGroup {
			for cPID, tgid := range f.PIDToTGID {
				if tgid == pid {
					f.PIDToCookie[cPID] = 0
				}
			}
		}
	}
	if len(failedPIDs) > 0 {
		return failedPIDs, fmt.Errorf("clear cookie error")
	}
	return nil, nil
}

func (f *FakeCoreSchedExtended) Assign(pidTypeFrom CoreSchedScopeType, pidFrom uint32, pidTypeTo CoreSchedScopeType, pidsTo ...uint32) ([]uint32, error) {
	var failedPIDs []uint32
	if pidTypeFrom != CoreSchedScopeThread {
		return nil, fmt.Errorf("unsupported pid type %d", pidTypeFrom)
	}
	if _, ok := f.PIDToError[pidFrom]; ok {
		return nil, fmt.Errorf("assign cookie for pidFrom error")
	}
	cookieID := f.PIDToCookie[pidFrom]
	for _, pidTo := range pidsTo {
		if _, ok := f.PIDToError[pidTo]; ok {
			failedPIDs = append(failedPIDs, pidTo)
			continue
		}
		if pidTypeTo == CoreSchedScopeThreadGroup {
			f.PIDToCookie[pidTo] = cookieID
			continue
		}
		if pidTypeTo == CoreSchedScopeProcessGroup {
			for cPID, pgid := range f.PIDToPGID {
				if pgid != pidTo {
					continue
				}
				if _, ok := f.PIDToError[cPID]; ok {
					failedPIDs = append(failedPIDs, cPID)
					continue
				}
				f.PIDToCookie[cPID] = cookieID
			}
		} else if pidTypeTo == CoreSchedScopeThreadGroup {
			for cPID, tgid := range f.PIDToTGID {
				if tgid != pidTo {
					continue
				}
				if _, ok := f.PIDToError[cPID]; ok {
					failedPIDs = append(failedPIDs, cPID)
					continue
				}
				f.PIDToCookie[cPID] = cookieID
			}
		}
	}
	if len(failedPIDs) > 0 {
		return failedPIDs, fmt.Errorf("assign cookie for pidsTo error")
	}
	return nil, nil
}

// EnableCoreSchedIfSupported checks if the core scheduling feature is enabled in the kernel sched_features.
// If kernel supported (available in the latest Anolis OS), it tries to enable the core scheduling feature.
// The core sched's kernel feature is known set in two places, if both of them are not found, the system is considered
// unsupported for the core scheduling:
//  1. In `/proc/sys/kernel/sched_core`, the value `1` means the feature is enabled while `0` means disabled.
//  2. (Older kernel) In `/sys/kernel/debug/sched_features`, the field `CORE_SCHED` means the feature is enabled while `NO_CORE_SCHED`
//     means it is disabled.
func EnableCoreSchedIfSupported() (bool, string) {
	// 1. try sysctl
	isSysctlSupported, err := GetSchedCore()
	if err == nil && isSysctlSupported {
		klog.V(6).Infof("Core Sched is already enabled by sysctl")
		return true, ""
	}
	if err == nil { // sysctl supported while value=0
		klog.V(6).Infof("Core Sched is disabled by sysctl, try to enable it")
		err = SetSchedCore(true)
		if err == nil {
			klog.V(4).Infof("Core Sched is enabled by sysctl successfully")
			return true, ""
		}
		klog.V(4).Infof("failed to enable core sched via sysctl, fallback to sched_features, err: %s", err)
	} else {
		klog.V(5).Infof("failed to enable core sched via sysctl since get failed, try sched_features, err: %s", err)
	}

	// 2. try sched_features (old interface)
	isSchedFeaturesSuppported, msg := SchedFeatures.IsSupported("")
	if !isSchedFeaturesSuppported { // sched_features not exist
		klog.V(6).Infof("failed to enable core sched via sysctl or sched_features, feature unsupported, msg: %s", msg)
		return false, "core sched not supported"
	}
	isSchedFeatureEnabled, err := IsCoreSchedFeatureEnabled()
	if err == nil && isSchedFeatureEnabled {
		klog.V(6).Infof("Core Sched is already enabled by sched_features")
		return true, ""
	}
	if err == nil {
		klog.V(6).Infof("Core Sched is disabled by sched_features, try to enable it")
		isSchedFeatureEnabled, msg = SetCoreSchedFeatureEnabled()
		if isSchedFeatureEnabled {
			klog.V(4).Infof("Core Sched is enabled by sched_features successfully")
			return true, ""
		}
		klog.V(4).Infof("failed to enable core sched via sched_features, msg: %s", msg)
	} else {
		klog.V(5).Infof("failed to enable core sched via sched_features, err: %s", err)
	}

	return false, "core sched not supported"
}

func IsCoreSchedFeatureEnabled() (bool, error) {
	featurePath := SchedFeatures.Path("")
	content, err := os.ReadFile(featurePath)
	if err != nil {
		return false, fmt.Errorf("failed to read sched_features, err: %w", err)
	}

	features := strings.Fields(string(content))
	for _, feature := range features {
		if feature == SchedFeatureCoreSched {
			klog.V(6).Infof("Core Sched is enabled by sched_features")
			return true, nil
		} else if feature == SchedFeatureNoCoreSched {
			klog.V(6).Infof("Core Sched is disabled by sched_features")
			return false, nil
		}
	}

	return false, fmt.Errorf("core sched not found in sched_features")
}

// SetCoreSchedFeatureEnabled checks if the core scheduling feature can be enabled in the kernel sched_features.
func SetCoreSchedFeatureEnabled() (bool, string) {
	featurePath := SchedFeatures.Path("")
	content, err := os.ReadFile(featurePath)
	if err != nil {
		klog.V(5).Infof("Core Sched is unsupported by sched_features %s, read err: %s", featurePath, err)
		return false, fmt.Sprintf("failed to read sched_features")
	}

	features := strings.Fields(string(content))
	for _, feature := range features {
		if feature == SchedFeatureCoreSched {
			return true, ""
		}
	}

	err = os.WriteFile(featurePath, []byte(fmt.Sprintf("%s\n", SchedFeatureCoreSched)), 0666)
	if err != nil {
		klog.V(5).Infof("Core Sched is unsupported by sched_features %s, write err: %s", featurePath, err)
		return false, fmt.Sprintf("failed to write sched_features")
	}

	return true, ""
}

const (
	// VirtualCoreSchedCookieName is the name of a virtual system resource for the core scheduling cookie.
	VirtualCoreSchedCookieName = "core_sched_cookie"

	// DefaultCoreSchedCookieID is the default cookie of the core scheduling.
	DefaultCoreSchedCookieID uint64 = 0
)

var (
	// VirtualCoreSchedCookie represents a virtual system resource for the core scheduling cookie.
	// It is virtual for denoting the operation on processes' core scheduling cookie, and it is not allowed to do
	// any real read or write on the provided filepath.
	VirtualCoreSchedCookie = NewCommonSystemResource("", VirtualCoreSchedCookieName, GetProcRootDir)
)
