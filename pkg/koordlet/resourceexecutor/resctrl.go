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

package resourceexecutor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const ErrResctrlDir = "resctrl path or file not exist"
const CacheIdIndex = 2

func NewResctrlReader() ResctrlReader {
	if vendorId, err := system.GetVendorIDByCPUInfo(system.GetCPUInfoPath()); err != nil {
		// FIXME: should we panic there?
		klog.V(0).ErrorS(err, "get cpu vendor error")
		return nil
	} else {
		switch vendorId {
		case system.INTEL_VENDOR_ID:
			return NewResctrlRDTReader()
		case system.AMD_VENDOR_ID:
			return &ResctrlAMDReader{}
		default:
			klog.V(0).ErrorS(err, "unsupported cpu vendor")
		}
	}
	return &fakeReader{}
}

type CacheId int

// parent for resctrl is like: ``, `BE`, `LS`
type ResctrlReader interface {
	ReadResctrlL3Stat(parent string) (map[CacheId]uint64, error)
	ReadResctrlMBStat(parent string) (map[CacheId]system.MBStatData, error)
}

type ResctrlRDTReader struct{}
type ResctrlAMDReader struct {
	ResctrlRDTReader
}

type fakeReader struct{}

func (rr *fakeReader) ReadResctrlL3Stat(parent string) (map[CacheId]uint64, error) {
	return nil, errors.New("unsupported platform")
}

func (rr *fakeReader) ReadResctrlMBStat(parent string) (map[CacheId]system.MBStatData, error) {
	return nil, errors.New("unsupported platform")
}

func NewResctrlRDTReader() ResctrlReader {
	return &ResctrlRDTReader{}
}

func (rr *ResctrlRDTReader) ReadResctrlL3Stat(parent string) (map[CacheId]uint64, error) {
	l3Stat := make(map[CacheId]uint64)
	monDataPath := system.GetResctrlMonDataPath(parent)
	fd, err := os.Open(monDataPath)
	if err != nil {
		return nil, fmt.Errorf(ErrResctrlDir)
	}
	// read all l3-memory domains
	domains, err := fd.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("%s, cannot find L3 domains, err: %w", ErrResctrlDir, err)
	}
	for _, domain := range domains {
		cacheId, err := strconv.Atoi(strings.Split(domain.Name(), "_")[CacheIdIndex])
		if err != nil {
			return nil, fmt.Errorf("%s, cannot get cacheid, err: %w", ErrResctrlDir, err)
		}
		path := system.ResctrlLLCOccupancy.Path(filepath.Join(parent, system.ResctrlMonData, domain.Name()))
		l3Byte, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s, cannot read from resctrl file system, err: %w",
				ErrResctrlDir, err)
		}
		l3Usage, err := strconv.ParseUint(string(l3Byte), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot parse result, err: %w", err)
		}
		l3Stat[CacheId(cacheId)] = l3Usage
	}
	return l3Stat, nil
}

func (rr *ResctrlRDTReader) ReadResctrlMBStat(parent string) (map[CacheId]system.MBStatData, error) {
	mbStat := make(map[CacheId]system.MBStatData)
	monDataPath := system.GetResctrlMonDataPath(parent)
	fd, err := os.Open(monDataPath)
	if err != nil {
		return nil, fmt.Errorf(ErrResctrlDir)
	}
	// read all l3-memory domains
	domains, err := fd.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("%s, cannot find L3 domains, err: %w", ErrResctrlDir, err)
	}
	for _, domain := range domains {
		cacheId, err := strconv.Atoi(strings.Split(domain.Name(), "_")[CacheIdIndex])
		if err != nil {
			return nil, fmt.Errorf("%s, cannot get cacheid, err: %w", ErrResctrlDir, err)
		}
		mbStat[CacheId(cacheId)] = make(system.MBStatData)
		for _, mbResource := range []system.Resource{
			system.ResctrlMBLocal, system.ResctrlMBTotal,
		} {
			contentName := mbResource.Path(filepath.Join(parent, system.ResctrlMonData, domain.Name()))
			contentByte, err := os.ReadFile(contentName)
			if err != nil {
				return nil, fmt.Errorf("%s, cannot read from resctrl file system, err: %w",
					ErrResctrlDir, err)
			}
			mbUsage, err := strconv.ParseUint(string(contentByte), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse result, err: %w", err)
			}
			mbStat[CacheId(cacheId)][string(mbResource.ResourceType())] = mbUsage
		}
	}
	return mbStat, nil
}
