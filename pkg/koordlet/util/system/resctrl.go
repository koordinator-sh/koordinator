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
	"bufio"
	"fmt"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	ResctrlName string = "resctrl"

	ResctrlDir string = "resctrl/"
	RdtInfoDir string = "info"
	L3CatDir   string = "L3"

	ResctrlSchemataName string = "schemata"
	ResctrlCbmMaskName  string = "cbm_mask"
	ResctrlTasksName    string = "tasks"

	// L3SchemataPrefix is the prefix of l3 cat schemata
	L3SchemataPrefix = "L3"
	// MbSchemataPrefix is the prefix of mba schemata
	MbSchemataPrefix = "MB"

	// other cpu vendor like "GenuineIntel"
	AMD_VENDOR_ID   = "AuthenticAMD"
	INTEL_VENDOR_ID = "GenuineIntel"
)

var (
	initLock          sync.Mutex
	isInit            bool
	isSupportResctrl  bool
	CacheIdsCacheFunc func() ([]int, error)
)

func init() {
	CacheIdsCacheFunc = util.OnceValues(GetCacheIds)
}

func isCPUSupportResctrl() (bool, error) {
	isCatFlagSet, isMbaFlagSet, err := isResctrlAvailableByCpuInfo(GetCPUInfoPath())
	if err != nil {
		klog.Errorf("isResctrlAvailableByCpuInfo error: %v", err)
		return false, err
	}
	klog.V(4).Infof("isResctrlAvailableByCpuInfo result, isCatFlagSet: %v, isMbaFlagSet: %v", isCatFlagSet, isMbaFlagSet)
	return isCatFlagSet && isMbaFlagSet, nil
}

func isKernelSupportResctrl() (bool, error) {
	if vendorID, err := GetVendorIDByCPUInfo(GetCPUInfoPath()); err == nil && vendorID == AMD_VENDOR_ID {
		// AMD CPU support resctrl by default
		klog.V(4).Infof("isKernelSupportResctrl true, since the cpu vendor is %v, no need to check kernel command line", vendorID)
		return true, nil
	}
	isCatFlagSet, isMbaFlagSet, err := isResctrlAvailableByKernelCmd(filepath.Join(Conf.ProcRootDir, KernelCmdlineFileName))
	if err != nil {
		klog.Errorf("isResctrlAvailableByKernelCmd error: %v", err)
		return false, err
	}
	klog.V(4).Infof("isResctrlAvailableByKernelCmd result, isCatFlagSet: %v, isMbaFlagSet: %v", isCatFlagSet, isMbaFlagSet)
	return isCatFlagSet && isMbaFlagSet, nil
}

func IsSupportResctrl() (bool, error) {
	initLock.Lock()
	defer initLock.Unlock()
	if !isInit {
		cpuSupport, err := isCPUSupportResctrl()
		if err != nil {
			return false, err
		}
		kernelCmdSupport, err := isKernelSupportResctrl()
		if err != nil {
			return false, err
		}
		// Kernel cmdline not set resctrl features does not ensure feature must be disabled.
		klog.Infof("IsSupportResctrl result, cpuSupport: %v, kernelSupport: %v",
			cpuSupport, kernelCmdSupport)
		isSupportResctrl = cpuSupport
		isInit = true
	}
	return isSupportResctrl, nil
}

var (
	ResctrlRoot      = NewCommonResctrlResource("", "")
	ResctrlSchemata  = NewCommonResctrlResource(ResctrlSchemataName, "")
	ResctrlTasks     = NewCommonResctrlResource(ResctrlTasksName, "")
	ResctrlL3CbmMask = NewCommonResctrlResource(ResctrlCbmMaskName, filepath.Join(RdtInfoDir, L3CatDir))
)

var _ Resource = &ResctrlResource{}

type ResctrlResource struct {
	Type           ResourceType
	FileName       string
	Subdir         string
	CheckSupported func(r Resource, parentDir string) (isSupported bool, msg string)
	Validator      ResourceValidator
}

func (r *ResctrlResource) ResourceType() ResourceType {
	if len(r.Type) > 0 {
		return r.Type
	}
	return ResourceType(filepath.Join(r.Subdir, r.FileName))
}

func (r *ResctrlResource) Path(parentDir string) string {
	// parentDir for resctrl is like: `/`, `LS/`, `BE`
	return filepath.Join(Conf.SysFSRootDir, ResctrlDir, parentDir, r.Subdir, r.FileName)
}

func (r *ResctrlResource) IsSupported(parentDir string) (bool, string) {
	if r.CheckSupported == nil {
		return true, ""
	}
	return r.CheckSupported(r, parentDir)
}

func (r *ResctrlResource) IsValid(v string) (bool, string) {
	if r.Validator == nil {
		return true, ""
	}
	return r.Validator.Validate(v)
}

func (r *ResctrlResource) WithValidator(validator ResourceValidator) Resource {
	r.Validator = validator
	return r
}

func (r *ResctrlResource) WithSupported(isSupported bool, msg string) Resource {
	return r
}

func (r *ResctrlResource) WithCheckSupported(checkSupportedFn func(r Resource, parentDir string) (isSupported bool, msg string)) Resource {
	r.CheckSupported = checkSupportedFn
	return r
}

func (r *ResctrlResource) WithCheckOnce(isCheckOnce bool) Resource {
	return r
}

func NewCommonResctrlResource(filename string, subdir string) Resource {
	return &ResctrlResource{
		Type:           ResourceType(filename),
		FileName:       filename,
		Subdir:         subdir,
		CheckSupported: SupportedIfFileExists,
	}
}

type ResctrlSchemataRaw struct {
	L3    map[int]int64
	MB    map[int]int64
	L3Num int
}

func NewResctrlSchemataRaw(cacheids []int) *ResctrlSchemataRaw {
	r := &ResctrlSchemataRaw{
		L3:    make(map[int]int64),
		MB:    make(map[int]int64),
		L3Num: 1,
	}
	for _, id := range cacheids {
		r.L3[id] = 0
		r.MB[id] = 0
	}

	return r
}

func (r *ResctrlSchemataRaw) WithL3Num(l3Num int) *ResctrlSchemataRaw {
	r.L3Num = l3Num
	return r
}

func (r *ResctrlSchemataRaw) WithL3Mask(mask string) *ResctrlSchemataRaw {
	// l3 mask MUST be a valid hex
	maskValue, err := strconv.ParseInt(strings.TrimSpace(mask), 16, 64)
	if err != nil {
		klog.V(5).Infof("failed to parse l3 mask %s, err: %v", mask, err)
	}
	for id := range r.L3 {
		r.L3[id] = maskValue
	}
	return r
}

func (r *ResctrlSchemataRaw) WithMB(valueOrPercent string) *ResctrlSchemataRaw {
	// mba valueOrPercent MUST be a valid integer
	// for intel: "MB:0=100;1=100"
	// for amd format: "MB:0=2048;1=2048;2=2048;3=2048"
	percentValue, err := strconv.ParseInt(strings.TrimSpace(valueOrPercent), 10, 64)
	if err != nil {
		klog.V(5).Infof("failed to parse mba %s, err: %v", valueOrPercent, err)
	}
	for id := range r.MB {
		r.MB[id] = percentValue
	}
	return r
}

func (r *ResctrlSchemataRaw) DeepCopy() *ResctrlSchemataRaw {
	n := NewResctrlSchemataRaw(r.CacheIds()).WithL3Num(r.L3Num)
	for id := range r.L3 {
		n.L3[id] = r.L3[id]
		n.MB[id] = r.MB[id]
	}
	return n
}

func (r *ResctrlSchemataRaw) Prefix() string {
	var prefix string
	if len(r.L3) > 0 {
		prefix += L3SchemataPrefix + ":"
	}
	if len(r.MB) > 0 {
		prefix += MbSchemataPrefix + ":"
	}
	return prefix
}

func (r *ResctrlSchemataRaw) L3Number() int {
	return r.L3Num
}

func (r *ResctrlSchemataRaw) CacheIds() []int {
	// TODO: consider situation that L3 number and the MB number are the same.
	ids := []int{}
	for id := range r.L3 {
		ids = append(ids, id)
	}
	return ids
}

func (r *ResctrlSchemataRaw) L3String() string {
	if len(r.L3) <= 0 {
		return ""
	}
	schemata := L3SchemataPrefix + ":"
	// the last ';' will be auto ignored
	ids := r.CacheIds()
	sort.Ints(ids)
	for _, id := range ids {
		schemata = schemata + strconv.Itoa(id) + "=" + strconv.FormatInt(r.L3[id], 16) + ";"
	}
	// the trailing '\n' is necessary to append
	schemata += "\n"
	return schemata
}

func (r *ResctrlSchemataRaw) MBString() string {
	if len(r.MB) <= 0 {
		return ""
	}
	schemata := MbSchemataPrefix + ":"
	// the last ';' will be auto ignored
	ids := r.CacheIds()
	sort.Ints(ids)
	for _, id := range ids {
		schemata = schemata + strconv.Itoa(id) + "=" + strconv.FormatInt(r.MB[id], 10) + ";"
	}
	// the trailing '\n' is necessary to append
	schemata += "\n"
	return schemata
}

func (r *ResctrlSchemataRaw) Equal(a *ResctrlSchemataRaw) (bool, string) {
	if r.L3Num != a.L3Num {
		return false, "l3 number not equal"
	}
	if a.L3 != nil {
		if len(r.L3) != len(a.L3) || r.L3 == nil {
			return false, "the number of l3 masks not equal"
		}
		for i := 0; i < len(r.L3); i++ {
			if r.L3[i] != a.L3[i] {
				return false, "the value of l3 mask not equal"
			}
		}
	}
	if a.MB != nil {
		if len(r.MB) != len(a.MB) || r.MB == nil {
			return false, "the number of mba percent not equal"
		}
		for i := 0; i < len(r.MB); i++ {
			if r.MB[i] != a.MB[i] {
				return false, "the value of mba percent not equal"
			}
		}
	}
	return true, ""
}

func (r *ResctrlSchemataRaw) Validate() (bool, string) {
	if valid, msg := r.ValidateL3(); !valid {
		return false, fmt.Sprintf("L3 CAT is invalid, msg: %s", msg)
	}
	if valid, msg := r.ValidateMB(); !valid {
		return false, fmt.Sprintf("MBA is invalid, msg: %s", msg)
	}
	return true, ""
}

func (r *ResctrlSchemataRaw) ValidateL3() (bool, string) {
	if r.L3Num <= 0 {
		return false, "L3 number is zero"
	}
	if len(r.L3) <= 0 {
		return false, "no L3 CAT info"
	}
	if r.L3Num != len(r.L3) {
		return false, "unmatched L3 number and CAT infos"
	}
	for _, value := range r.L3 {
		if value <= 0 {
			return false, "wrong value of L3 mask"
		}
	}
	return true, ""
}

func (r *ResctrlSchemataRaw) ValidateMB() (bool, string) {
	if r.L3Num <= 0 {
		return false, "L3 number is zero"
	}
	if len(r.MB) <= 0 {
		return false, "no MBA info"
	}
	for _, value := range r.MB {
		if value <= 0 {
			return false, "wrong value of MB mask"
		}
	}
	return true, ""
}

// ParseResctrlSchemata parses the resctrl schemata of given cgroup, and returns the l3_cat masks and mba masks.
// Set l3Num=-1 to use the read L3 number from the schemata.
// @content `L3:0=fff;1=fff\nMB:0=100;1=100\n` (may have additional lines (e.g. ARM MPAM))
// @l3Num 2
// @return {L3: {0: "fff", 1: "fff"}, MB: {0: "100", 1: "100"}}, nil
func (r *ResctrlSchemataRaw) ParseResctrlSchemata(content string, l3Num int) error {
	schemataMap := ParseResctrlSchemataMap(content)

	for _, t := range []struct {
		prefix string
		base   int
		v      *map[int]int64
	}{
		{
			prefix: L3SchemataPrefix,
			base:   16,
			v:      &r.L3,
		},
		{
			prefix: MbSchemataPrefix,
			base:   10,
			v:      &r.MB,
		},
	} {
		maskMap := schemataMap[t.prefix]
		if maskMap == nil {
			klog.V(5).Infof("read resctrl schemata of %s aborted, mask not found", t.prefix)
			continue
		}
		if l3Num == -1 {
			l3Num = len(maskMap)
		}
		if len(maskMap) != l3Num {
			return fmt.Errorf("read resctrl schemata failed, %s masks has invalid count %v, l3Num %v",
				t.prefix, len(maskMap), l3Num)
		}
		for id, mask := range maskMap {
			maskValue, err := strconv.ParseInt(strings.TrimSpace(mask), t.base, 64)
			if err != nil {
				return fmt.Errorf("read resctrl schemata failed, %s masks is invalid, value %s, err: %s",
					t.prefix, mask, err)
			}
			(*t.v)[id] = maskValue
		}
	}

	return nil
}

// @return /sys/fs/resctrl
func GetResctrlSubsystemDirPath() string {
	return filepath.Join(Conf.SysFSRootDir, ResctrlDir)
}

// @groupPath BE
// @return /sys/fs/resctrl/BE
func GetResctrlGroupRootDirPath(groupPath string) string {
	return filepath.Join(Conf.SysFSRootDir, ResctrlDir, groupPath)
}

// @return /sys/fs/resctrl/info/L3/cbm_mask
func GetResctrlL3CbmFilePath() string {
	return ResctrlL3CbmMask.Path("")
}

// @groupPath BE
// @return /sys/fs/resctrl/BE/schemata
func GetResctrlSchemataFilePath(groupPath string) string {
	return ResctrlSchemata.Path(groupPath)
}

// @groupPath BE
// @return /sys/fs/resctrl/BE/tasks
func GetResctrlTasksFilePath(groupPath string) string {
	return ResctrlTasks.Path(groupPath)
}

func ReadResctrlSchemataRaw(schemataFile string, l3Num int) (*ResctrlSchemataRaw, error) {
	content, err := os.ReadFile(schemataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read schemata file, err: %v", err)
	}

	schemataRaw := NewResctrlSchemataRaw([]int{})
	err = schemataRaw.ParseResctrlSchemata(string(content), l3Num)
	if err != nil {
		return nil, fmt.Errorf("failed to parse l3 schemata, content %s, err: %v", string(content), err)
	}
	if l3Num == -1 {
		schemataRaw.WithL3Num(len(schemataRaw.L3))
	}

	return schemataRaw, nil
}

// GetCacheIds get cache ids from schemata file.
// e.g. schemata=`L3:0=fff;1=fff\nMB:0=100;1=100\n` -> [0, 1]
func GetCacheIds() ([]int, error) {
	r, err := ReadResctrlSchemataRaw(filepath.Join(Conf.SysFSRootDir, ResctrlDir, ResctrlSchemataName), -1)
	if err != nil {
		return nil, err
	}
	return r.CacheIds(), nil
}

// ParseResctrlSchemataMap parses the content of resctrl schemata.
// e.g. schemata=`L3:0=fff;1=fff\nMB:0=100;1=100\n` -> `{"L3": {0: "fff", 1: "fff"}, "MB": {0: "100", 1: "100"}}`
func ParseResctrlSchemataMap(content string) map[string]map[int]string {
	schemataMap := map[string]map[int]string{}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line) // `L3:0=fff;1=fff`, `MB:0=100;1=100`
		if len(line) <= 0 {
			continue
		}

		pair := strings.Split(line, ":") // {`L3`, `0=fff;1=fff`}, {`MB`, `0=100;1=100`}
		if len(pair) != 2 {
			klog.V(6).Infof("failed to parse resctrl schemata, line %s, err: invalid key value pair", line)
			continue
		}

		masks := strings.Split(pair[1], ";") // {`0=fff`, `1=fff`}, {`0=100`, `1=100`}
		maskMap := map[int]string{}
		for _, mask := range masks {
			maskPair := strings.Split(mask, "=") // {`0`, `fff`}, {`1`, `100`}
			if len(maskPair) != 2 {
				klog.V(6).Infof("failed to parse resctrl schemata, mask %s, err: invalid key value pair", mask)
				continue
			}
			nodeID, err := strconv.ParseInt(maskPair[0], 10, 32)
			if err != nil {
				klog.V(6).Infof("failed to parse resctrl schemata, mask %s, err: %s", mask, err)
				continue
			}
			maskMap[int(nodeID)] = maskPair[1] // {0: `fff`}
		}
		schemataMap[pair[0]] = maskMap // {`L3`: {0: `fff`, 1: `fff`} }
	}

	return schemataMap
}

// ReadCatL3CbmString reads and returns the value of cat l3 cbm_mask
func ReadCatL3CbmString() (string, error) {
	cbmFile := GetResctrlL3CbmFilePath()
	out, err := os.ReadFile(cbmFile)
	if err != nil {
		return "", fmt.Errorf("failed to read l3 cbm, path %s, err: %v", cbmFile, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ReadResctrlTasksMap reads and returns the map of given resctrl group's task ids
func ReadResctrlTasksMap(groupPath string) (map[int32]struct{}, error) {
	tasksPath := GetResctrlTasksFilePath(groupPath)
	rawContent, err := os.ReadFile(tasksPath)
	if err != nil {
		return nil, err
	}

	tasksMap := map[int32]struct{}{}

	lines := strings.Split(string(rawContent), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) <= 0 {
			continue
		}
		task, err := strconv.ParseInt(line, 10, 32)
		if err != nil {
			return nil, err
		}
		tasksMap[int32(task)] = struct{}{}
	}
	return tasksMap, nil
}

// CheckAndTryEnableResctrlCat checks if resctrl and l3_cat are enabled; if not, try to enable the features by mount
// resctrl subsystem; See MountResctrlSubsystem() for the detail.
// It returns whether the resctrl cat is enabled, and the error if failed to enable or to check resctrl interfaces
func CheckAndTryEnableResctrlCat() error {
	// resctrl cat is correctly enabled: l3_cbm path exists
	l3CbmFilePath := GetResctrlL3CbmFilePath()
	_, err := os.Stat(l3CbmFilePath)
	if err == nil {
		return nil
	}
	klog.V(5).Infof("resctrl l3 cbm is not exit, err=%s, try to mount resctrl", err)
	newMount, err := MountResctrlSubsystem()
	if err != nil {
		return err
	}
	if newMount {
		klog.Infof("mount resctrl successfully, resctrl enabled")
	}
	// double check l3_cbm path to ensure both resctrl and cat are correctly enabled
	l3CbmFilePath = GetResctrlL3CbmFilePath()
	_, err = os.Stat(l3CbmFilePath)
	if err != nil {
		return fmt.Errorf("resctrl cat is not enabled, err: %s", err)
	}
	return nil
}

func InitCatGroupIfNotExist(group string) (bool, error) {
	path := GetResctrlGroupRootDirPath(group)
	_, err := os.Stat(path)
	if err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("check dir %v for group %s but got unexpected err: %v", path, group, err)
	}
	err = os.Mkdir(path, 0755)
	if err != nil {
		return false, fmt.Errorf("create dir %v failed for group %s, err: %v", path, group, err)
	}
	return true, nil
}

func CheckResctrlSchemataValid() error {
	schemataPath := GetResctrlSchemataFilePath("")
	schemataRaw, err := ReadResctrlSchemataRaw(schemataPath, -1)
	if err != nil {
		return fmt.Errorf("cannot read resctrl schemata, err: %s", err)
	}
	isValid, msg := schemataRaw.Validate()
	if !isValid {
		return fmt.Errorf("resctrl schemata is invalid, err: %s", msg)
	}
	return nil
}

func CalculateCatL3MaskValue(cbm uint, startPercent, endPercent int64) (string, error) {
	// check if the parsed cbm value is valid, eg. 0xff, 0x1, 0x7ff, ...
	// NOTE: (Cache Bit Masks) X86 hardware requires that these masks have all the '1' bits in a contiguous block.
	//       ref: https://www.kernel.org/doc/Documentation/x86/intel_rdt_ui.txt
	// since the input cbm here is the cbm value of the resctrl root, every lower bit is required to be `1` additionally
	if bits.OnesCount(cbm+1) != 1 {
		return "", fmt.Errorf("illegal cbm %v", cbm)
	}

	// check if the startPercent and endPercent are valid
	if startPercent < 0 || endPercent > 100 || endPercent <= startPercent {
		return "", fmt.Errorf("illegal l3 cat percent: start %v, end %v", startPercent, endPercent)
	}

	// calculate a bit mask belonging to interval [startPercent% * ways, endPercent% * ways)
	// eg.
	// cbm 0x3ff ('b1111111111), start 10%, end 80%
	// ways 10, l3Mask 0xfe ('b11111110)
	// cbm 0x7ff ('b11111111111), start 10%, end 50%
	// ways 11, l3Mask 0x3c ('b111100)
	// cbm 0x7ff ('b11111111111), start 0%, end 30%
	// ways 11, l3Mask 0xf ('b1111)
	ways := float64(bits.Len(cbm))
	startWay := uint64(math.Ceil(ways * float64(startPercent) / 100))
	endWay := uint64(math.Ceil(ways * float64(endPercent) / 100))

	var l3Mask uint64 = (1 << endWay) - (1 << startWay)
	return strconv.FormatUint(l3Mask, 16), nil
}

// GetVendorIDByCPUInfo returns vendor_id like AuthenticAMD from cpu info, e.g.
// vendor_id       : AuthenticAMD
// vendor_id       : GenuineIntel
func GetVendorIDByCPUInfo(path string) (string, error) {
	vendorID := "unknown"
	f, err := os.Open(path)
	if err != nil {
		return vendorID, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		if err := s.Err(); err != nil {
			return vendorID, err
		}

		line := s.Text()

		// get "vendor_id" from first line
		if strings.Contains(line, "vendor_id") {
			attrs := strings.Split(line, ":")
			if len(attrs) >= 2 {
				vendorID = strings.TrimSpace(attrs[1])
				break
			}
		}
	}
	return vendorID, nil
}

func isResctrlAvailableByCpuInfo(path string) (bool, bool, error) {
	isCatFlagSet := false
	isMbaFlagSet := false

	f, err := os.Open(path)
	if err != nil {
		return false, false, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		if err := s.Err(); err != nil {
			return false, false, err
		}

		line := s.Text()

		// Search "cat_l3" and "mba" flags in first "flags" line
		if strings.Contains(line, "flags") {
			flags := strings.Split(line, " ")
			// "cat_l3" flag for CAT and "mba" flag for MBA
			for _, flag := range flags {
				switch flag {
				case "cat_l3":
					isCatFlagSet = true
				case "mba":
					isMbaFlagSet = true
				}
			}
			return isCatFlagSet, isMbaFlagSet, nil
		}
	}
	return isCatFlagSet, isMbaFlagSet, nil
}

// file content example:
// BOOT_IMAGE=/boot/vmlinuz-4.19.91-24.1.al7.x86_64 root=UUID=231efa3b-302b-4e82-9445-0f7d5d353dda \
// crashkernel=0M-2G:0M,2G-8G:192M,8G-:256M cryptomgr.notests cgroup.memory=nokmem rcupdate.rcu_cpu_stall_timeout=300 \
// vring_force_dma_api biosdevname=0 net.ifnames=0 console=tty0 console=ttyS0,115200n8 noibrs \
// nvme_core.io_timeout=4294967295 nomodeset intel_idle.max_cstate=1 rdt=cmt,l3cat,l3cdp,mba
func isResctrlAvailableByKernelCmd(path string) (bool, bool, error) {
	isCatFlagSet := false
	isMbaFlagSet := false
	f, err := os.Open(path)
	if err != nil {
		return false, false, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if err := s.Err(); err != nil {
			return false, false, err
		}
		line := s.Text()
		l3Reg, regErr := regexp.Compile(".* rdt=.*l3cat.*")
		if regErr == nil && l3Reg.Match([]byte(line)) {
			isCatFlagSet = true
		}

		mbaReg, regErr := regexp.Compile(".* rdt=.*mba.*")
		if regErr == nil && mbaReg.Match([]byte(line)) {
			isMbaFlagSet = true
		}
	}
	return isCatFlagSet, isMbaFlagSet, nil
}
