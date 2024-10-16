package helper

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	configuredVfFile = "sriov_numvfs"
)

// SriovConfigured returns true if sriov_numvfs reads > 0 else false
func SriovConfigured(addr string) bool {
	return GetVConfigured(addr) > 0
}

func extractNumber(pfDir string, s string) int {
	num, _ := strconv.Atoi(strings.TrimPrefix(s, fmt.Sprintf("%s/virtfn", pfDir)))
	return num
}

// GetVFList returns a List containing PCI addr for all VF discovered in a given PF
func GetVFList(pf string) (vfList []string, err error) {
	vfList = make([]string, 0)
	pfDir := filepath.Join(SysBusPci, pf)
	_, err = os.Lstat(pfDir)
	if err != nil {
		err = fmt.Errorf("error. Could not get PF directory information for device: %s, Err: %v", pf, err)
		return
	}

	vfDirs, err := filepath.Glob(filepath.Join(pfDir, "virtfn*"))

	if err != nil {
		err = fmt.Errorf("error reading VF directories %v", err)
		return
	}
	//TODO 排序
	sort.Slice(vfDirs, func(i, j int) bool {
		return extractNumber(pfDir, vfDirs[i]) < extractNumber(pfDir, vfDirs[j])
	})

	// Read all VF directory and get add VF PCI addr to the vfList
	for _, dir := range vfDirs {
		dirInfo, err := os.Lstat(dir)
		if err == nil && (dirInfo.Mode()&os.ModeSymlink != 0) {
			linkName, err := filepath.EvalSymlinks(dir)
			if err == nil {
				vfLink := filepath.Base(linkName)
				vfList = append(vfList, vfLink)
			}
		}
	}
	return
}

// GetVConfigured returns number of VF configured for a PF
func GetVConfigured(pf string) int {
	configuredVfPath := filepath.Join(SysBusPci, pf, configuredVfFile)
	vfs, err := os.ReadFile(configuredVfPath)
	if err != nil {
		return 0
	}
	configuredVFs := bytes.TrimSpace(vfs)
	numConfiguredVFs, err := strconv.Atoi(string(configuredVFs))
	if err != nil {
		return 0
	}
	return numConfiguredVFs
}

// IsSriovVF check if a pci device has link to a PF
func IsSriovVF(pciAddr string) bool {
	totalVfFilePath := filepath.Join(SysBusPci, pciAddr, "physfn")
	if _, err := os.Stat(totalVfFilePath); err != nil {
		return false
	}
	return true
}
