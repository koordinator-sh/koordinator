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
	"unsafe"

	"golang.org/x/sys/unix"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
)

type CoreSched struct{}

func NewCoreSched() CoreSchedInterface {
	return &CoreSched{}
}

func NewCoreSchedExtended() CoreSchedExtendedInterface {
	return &CoreSched{}
}

func (s *CoreSched) Get(pidType CoreSchedScopeType, pid uint32) (uint64, error) {
	// NOTE: pidType only support Thread type.
	cookie := uint64(0)
	cookiePtr := &cookie
	ret, err := unix.PrctlRetInt(unix.PR_SCHED_CORE, unix.PR_SCHED_CORE_GET, uintptr(pid), uintptr(pidType), uintptr(unsafe.Pointer(cookiePtr)))
	if err != nil {
		return 0, fmt.Errorf("CoreSched get error, PID_TYPE=%v, PID=%v, err: %w", pidType, pid, err)
	}
	if ret != 0 {
		return 0, fmt.Errorf("CoreSched get failed, PID_TYPE=%v, PID=%v, ret: %v", pidType, pid, ret)
	}
	return cookie, nil
}

func (s *CoreSched) Create(pidType CoreSchedScopeType, pid uint32) error {
	ret, err := unix.PrctlRetInt(unix.PR_SCHED_CORE, unix.PR_SCHED_CORE_CREATE, uintptr(pid), uintptr(pidType), 0)
	if err != nil {
		return fmt.Errorf("CoreSched create error, PID_TYPE=%v, PID=%v, err: %w", pidType, pid, err)
	}
	if ret != 0 {
		return fmt.Errorf("CoreSched create failed, PID_TYPE=%v, PID=%v, ret: %v", pidType, pid, ret)
	}
	return nil
}

func (s *CoreSched) ShareTo(pidType CoreSchedScopeType, pid uint32) error {
	ret, err := unix.PrctlRetInt(unix.PR_SCHED_CORE, unix.PR_SCHED_CORE_SHARE_TO, uintptr(pid), uintptr(pidType), 0)
	if err != nil {
		return fmt.Errorf("CoreSched shareTo error, PID_TYPE=%v, PID=%v, err: %w", pidType, pid, err)
	}
	if ret != 0 {
		return fmt.Errorf("CoreSched shareTo failed, PID_TYPE=%v, PID=%v, ret: %v", pidType, pid, ret)
	}
	return nil
}

func (s *CoreSched) ShareFrom(pidType CoreSchedScopeType, pid uint32) error {
	// NOTE: pidTypeFrom only support Thread type.
	ret, err := unix.PrctlRetInt(unix.PR_SCHED_CORE, unix.PR_SCHED_CORE_SHARE_FROM, uintptr(pid), uintptr(pidType), 0)
	if err != nil {
		return fmt.Errorf("CoreSched shareFrom error, PID_TYPE=%v, PID=%v, err: %w", pidType, pid, err)
	}
	if ret != 0 {
		return fmt.Errorf("CoreSched shareFrom failed, PID_TYPE=%v, PID=%v, ret: %v", pidType, pid, ret)
	}
	return nil
}

type CoreSchedExtendedResult struct {
	FailedPIDs []uint32
	Error      error
}

func (s *CoreSched) clear(pidType CoreSchedScopeType, pids ...uint32) ([]uint32, error) {
	var failedPIDs []uint32
	var errs []error
	for _, pid := range pids {
		err := s.ShareTo(pidType, pid)
		if err != nil {
			failedPIDs = append(failedPIDs, pid)
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return failedPIDs, utilerrors.NewAggregate(errs)
	}
	return nil, nil
}

func (s *CoreSched) Clear(pidType CoreSchedScopeType, pids ...uint32) ([]uint32, error) {
	// keep the outside goroutine with cookie 0, then we can reset the target pid's cookie by ShareTo the cookie of
	// the new goroutine
	// TODO: directly use syscall when the kernel supports Clear (0x1000)
	retIf := GoWithNewThread(func() interface{} {
		failedPIDs, err := s.clear(pidType, pids...)
		if err != nil {
			return &CoreSchedExtendedResult{
				FailedPIDs: failedPIDs,
				Error:      err,
			}
		}
		return nil
	})
	if retIf == nil {
		return nil, nil
	}
	ret := retIf.(*CoreSchedExtendedResult)
	if ret != nil {
		return ret.FailedPIDs, fmt.Errorf("CoreSched Clear failed, err: %w", ret.Error)
	}
	return nil, nil
}

func (s *CoreSched) assign(pidTypeFrom CoreSchedScopeType, pidFrom uint32, pidTypeTo CoreSchedScopeType, pidsTo ...uint32) ([]uint32, error) {
	err := s.ShareFrom(pidTypeFrom, pidFrom)
	if err != nil {
		return nil, err
	}
	var failedPIDs []uint32
	var errs []error
	for _, pidTo := range pidsTo {
		err1 := s.ShareTo(pidTypeTo, pidTo)
		if err1 != nil {
			failedPIDs = append(failedPIDs, pidTo)
			errs = append(errs, err1)
		}
	}
	if len(errs) > 0 {
		return failedPIDs, utilerrors.NewAggregate(errs)
	}
	return nil, nil
}

func (s *CoreSched) Assign(pidTypeFrom CoreSchedScopeType, pidFrom uint32, pidTypeTo CoreSchedScopeType, pidsTo ...uint32) ([]uint32, error) {
	// keep the outside goroutine with cookie 0, then we can assign the pidFrom's cookie to pidTo by
	// NOTE: pidTypeFrom only support Thread type.
	// 1. ShareFrom the pidFrom's cookie to the new goroutine
	// 2. ShareTo the new goroutine's cookie to the target pidTo
	retIf := GoWithNewThread(func() interface{} {
		failedPIDs, err := s.assign(pidTypeFrom, pidFrom, pidTypeTo, pidsTo...)
		if err != nil {
			return &CoreSchedExtendedResult{
				FailedPIDs: failedPIDs,
				Error:      err,
			}
		}
		return nil
	})
	if retIf == nil {
		return nil, nil
	}
	ret := retIf.(*CoreSchedExtendedResult)
	if ret != nil {
		return ret.FailedPIDs, fmt.Errorf("CoreSched Clear failed, err: %w", ret.Error)
	}
	return nil, nil
}

// ProbeCoreSchedIfEnabled checks if the MAINLINE kernel support the core scheduling.
// Since there's no direct API, we probe by calling prctl and checking its return value.
func ProbeCoreSchedIfEnabled() bool {
	cookie := uint64(0)
	cookiePtr := &cookie
	ret, err := unix.PrctlRetInt(unix.PR_SCHED_CORE, unix.PR_SCHED_CORE_GET, uintptr(0), uintptr(CoreSchedScopeThread), uintptr(unsafe.Pointer(cookiePtr)))
	if err != nil {
		klog.V(4).Infof("failed to probe core sched status via prctl, err: %s", err)
		return false
	}
	return ret == 0
}
