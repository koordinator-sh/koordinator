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
	"sort"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

const (
	ProcStatName    = "stat"
	ProcMemInfoName = "meminfo"
	ProcCPUInfoName = "cpuinfo"
)

func GetProcFilePath(procRelativePath string) string {
	return filepath.Join(Conf.ProcRootDir, procRelativePath)
}

func GetProcRootDir() string {
	return Conf.ProcRootDir
}

// ProcStat is the content of /proc/<pid>/stat.
// https://manpages.ubuntu.com/manpages/xenial/en/man5/proc.5.html
type ProcStat struct {
	Pid   uint32
	Comm  string
	State byte
	Ppid  uint32
	Pgrp  uint32
	// TODO: add more fields if needed
}

func GetProcPIDStatPath(pid uint32) string {
	return filepath.Join(Conf.ProcRootDir, strconv.FormatUint(uint64(pid), 10), ProcStatName)
}

func ParseProcPIDStat(content string) (*ProcStat, error) {
	// pattern: `12345 (stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`
	// splitAfterComm -> "12345 (stress", " S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ..."
	splitAfterComm := strings.SplitN(content, ")", 2)
	if len(splitAfterComm) != 2 {
		return nil, fmt.Errorf("failed to parse stat, err: Comm not found")
	}
	// comm
	// splitBeforeComm -> "12345 ", "stress"
	splitBeforeComm := strings.SplitN(splitAfterComm[0], "(", 2)
	if len(splitBeforeComm) != 2 {
		return nil, fmt.Errorf("failed to parse stat, err: invalid Comm prefix %s", splitAfterComm[0])
	}
	stat := &ProcStat{}
	stat.Comm = splitBeforeComm[1]
	// pid
	trimPID := strings.TrimSpace(splitBeforeComm[0])
	pid, err := strconv.ParseUint(trimPID, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse stat, err: invalid pid %s", splitBeforeComm[0])
	}
	stat.Pid = uint32(pid)
	// fieldsAfterComm -> "S", "12340", "12344", "12340", "12300", "12345", "123450", "151", "0", ...
	fieldsAfterComm := strings.Fields(strings.TrimSpace(splitAfterComm[1]))
	if len(fieldsAfterComm) < 3 { // remaining fields are ignored
		return nil, fmt.Errorf("failed to parse stat, err: suffix fields not enough %s", splitAfterComm[1])
	}
	// state
	if len(fieldsAfterComm[0]) > 1 {
		return nil, fmt.Errorf("failed to parse stat, err: invalid state %s", fieldsAfterComm[0])
	}
	stat.State = fieldsAfterComm[0][0]
	// ppid
	ppid, err := strconv.ParseUint(fieldsAfterComm[1], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse stat, err: invalid ppid %s", fieldsAfterComm[1])
	}
	stat.Ppid = uint32(ppid)
	// pgrp/pgid
	pgrp, err := strconv.ParseUint(fieldsAfterComm[2], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse stat, err: invalid pgrp %s", fieldsAfterComm[2])
	}
	stat.Pgrp = uint32(pgrp)

	return stat, nil
}

func GetPGIDForPID(pid uint32) (uint32, error) {
	pidStatPath := GetProcPIDStatPath(pid)
	content, err := os.ReadFile(pidStatPath)
	if err != nil {
		return 0, err
	}
	stat, err := ParseProcPIDStat(string(content))
	if err != nil {
		return 0, err
	}
	return stat.Pgrp, nil
}

// GetPGIDsForPIDs gets the PGIDs for a cgroup's PIDs.
// It will consider the PID as PGID if its PGID does not exist anymore.
func GetPGIDsForPIDs(pids []uint32) ([]uint32, error) {
	pidMap := map[uint32]struct{}{}
	for _, pid := range pids {
		pidMap[pid] = struct{}{}
	}

	var pgids []uint32
	pgidMap := map[uint32]struct{}{}
	for _, pid := range pids {
		// get PGID (pgrp) via /proc/$pid/stat
		pgid, err := GetPGIDForPID(pid)
		if err != nil {
			klog.V(5).Infof("failed to get PGID for pid %v, err: %s", pid, err)
			continue
		}

		// verify if PGID lives in the pid list
		// if not, consider the PID as PGID
		_, ok := pidMap[pgid]
		if !ok {
			klog.V(6).Infof("failed to find PGID %v for pid %v, use pid as PGID", pgid, pid)
			pgid = pid
		}

		_, ok = pgidMap[pgid]
		if ok {
			continue
		}

		pgidMap[pgid] = struct{}{}
		pgids = append(pgids, pgid)
	}

	// in ascending order
	sort.Slice(pgids, func(i, j int) bool {
		return pgids[i] < pgids[j]
	})

	return pgids, nil
}

func GetContainerPGIDs(containerParentDir string) ([]uint32, error) {
	cgroupProcs, err := GetCgroupResource(CPUProcsName)
	if err != nil {
		return nil, err
	}

	cgroupProcsPath := cgroupProcs.Path(containerParentDir)
	rawContent, err := os.ReadFile(cgroupProcsPath)
	if err != nil {
		return nil, err
	}

	pids, err := ParseCgroupProcs(string(rawContent))
	if err != nil {
		return nil, err
	}

	return GetPGIDsForPIDs(pids)
}
