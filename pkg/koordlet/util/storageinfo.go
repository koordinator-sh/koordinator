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

package util

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

type LocalStorageInfo struct {
	// mapper of disk and disk number, such as "/dev/vda":"253:0"
	DiskNumberMap map[string]string
	// mapper of disk number and disk, such as "253:0":"/dev/vda"
	NumberDiskMap map[string]string
	// mapper of partition and its disk, such as "/dev/vdb3":"/dev/vdb"
	PartitionDiskMap map[string]string
	// mapper of volumegroup and its disk, such as "yoda-pool0":"/dev/vdb"
	VGDiskMap map[string]string
	// mapper of logicalvolume and its volumegroup, such as "/dev/mapper/yoda--pool0-yoda--2c52d97f--eab6--4ac5--ba8b--242f399470e1":"yoda-pool0"
	LVMapperVGMap map[string]string
	// mapper of mountpoint and its disk, such as "/var/lib/kubelet/pods/d806ee8d-fe28-4995-a836-d2356d44ec5f/volumes/kubernetes.io~csi/yoda-2c52d97f-eab6-4ac5-ba8b-242f399470e1/mount":"/dev/mapper/yoda--pool0-yoda--2c52d97f--eab6--4ac5--ba8b--242f399470e1"
	MPDiskMap map[string]string
}

var (
	deviceName    = "/dev/%s"
	lvmMapperName = "/dev/mapper/%s-%s"

	lsblkRE   = regexp.MustCompile(`([A-Z:]+)=(?:"(.*?)")`)
	vgsRE     = regexp.MustCompile(`[^\s]+`)
	lvsRE     = regexp.MustCompile(`[^\s]+`)
	findmntRE = regexp.MustCompile(`([A-Z:]+)=(?:"(.*?)")`)

	lsblkColumns = []string{
		"NAME",
		"TYPE",
		"MAJ:MIN",
	}
	vgsColumns = []string{
		"vg_name",
		"pv_count",
		"pv_name",
	}
	lvsColumns = []string{
		"lv_name",
		"vg_name",
	}
	findmntColumns = []string{
		"TARGET",
		"SOURCE",
	}
)

func (s *LocalStorageInfo) scanDevices() error {
	// A Cmd cannot be reused after calling its Run, Output or CombinedOutput methods.
	// Or it will report error: Stdout already set
	lsblkCommand := exec.Command(
		"lsblk",
		"-P", // output fields as key=value pairs
		"-o", strings.Join(lsblkColumns, ","),
	)
	output, err := lsblkCommand.Output()
	if err != nil {
		return fmt.Errorf("fail to exec lsblk: %s", err.Error())
	}
	return s.scanDevicesOutput(output)
}

func (s *LocalStorageInfo) scanVolumeGroups() error {
	vgsCommand := exec.Command(
		"vgs",
		"--noheadings",
		"--options", strings.Join(vgsColumns, ","),
	)
	output, err := vgsCommand.Output()
	if err != nil {
		return fmt.Errorf("fail to exec vgs: %s", err.Error())
	}
	return s.scanVolumeGroupsOutput(output)
}

func (s *LocalStorageInfo) scanLogicalVolumes() error {
	lvsCommand := exec.Command(
		"lvs",
		"--noheadings",
		"--options", strings.Join(lvsColumns, ","),
	)
	output, err := lvsCommand.Output()
	if err != nil {
		return fmt.Errorf("fail to exec lvs: %s", err.Error())
	}
	return s.scanLogicalVolumesOutput(output)
}

func (s *LocalStorageInfo) scanMountPoints() error {
	findmntCommand := exec.Command(
		"findmnt",
		"-P",
		"-o", strings.Join(findmntColumns, ","),
	)
	output, err := findmntCommand.Output()
	if err != nil {
		return fmt.Errorf("fail to exec findmnt: %s", err.Error())
	}
	return s.scanMountPointOutput(output)
}

func (s *LocalStorageInfo) scanDevicesOutput(output []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		// text: [[NAME="vdd" NAME vdd] [TYPE="disk" TYPE disk] [MAJ:MIN="253:48" MAJ:MIN 253:48]]
		text := lsblkRE.FindAllStringSubmatch(scanner.Text(), -1)
		if len(text) == 3 && len(text[0]) == 3 {
			switch text[1][2] {
			case "disk":
				s.DiskNumberMap[getDeviceName(text[0][2])] = text[2][2]
				s.NumberDiskMap[text[2][2]] = getDeviceName(text[0][2])
			case "part":
				s.PartitionDiskMap[getDeviceName(text[0][2])] = s.NumberDiskMap[getDiskNumberFromDeviceNumber(text[2][2])]
			}
		} else {
			return fmt.Errorf("output of lsblk is not correct: %v", text)
		}
	}
	return nil
}

func (s *LocalStorageInfo) scanVolumeGroupsOutput(output []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		// text: [yoda-pool0 1 /dev/vdc3]
		text := vgsRE.FindAllString(scanner.Text(), -1)
		if len(text) == 3 {
			if text[1] == "1" {
				if s.isDeviceDisk(text[2]) {
					s.VGDiskMap[text[0]] = text[2]
				} else {
					s.VGDiskMap[text[0]] = s.PartitionDiskMap[text[2]]
				}
			} else {
				klog.Warningf("now we only support pv_count == 1, output of vgs is %v", text)
			}
		} else {
			return fmt.Errorf("output of vgs is not correct: %v", text)
		}
	}
	return nil
}

func (s *LocalStorageInfo) scanLogicalVolumesOutput(output []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		// text: [yoda-15199981-7229-45e9-b3a0-b5b30a6a162b yoda-pool0]
		text := lvsRE.FindAllString(scanner.Text(), -1)
		if len(text) == 2 {
			s.LVMapperVGMap[getLVMMapperName(text[1], text[0])] = text[1]
		} else {
			return fmt.Errorf("output of lvs is not correct: %v", text)
		}
	}
	return nil
}

func (s *LocalStorageInfo) isDeviceDisk(device string) bool {
	_, yes := s.DiskNumberMap[device]
	return yes
}

func (s *LocalStorageInfo) scanMountPointOutput(output []byte) error {
	// text: [[TARGET="/var/lib/kubelet/pods/0ef5bd7a-aa83-4242-8597-c7ab4afaf356/volumes/kubernetes.io~csi/yoda-15199981-7229-45e9-b3a0-b5b30a6a162b/mount" TARGET /var/lib/kubelet/pods/0ef5bd7a-aa83-4242-8597-c7ab4afaf356/volumes/kubernetes.io~csi/yoda-15199981-7229-45e9-b3a0-b5b30a6a162b/mount] [SOURCE="/dev/mapper/yoda--pool0-yoda--15199981--7229--45e9--b3a0--b5b30a6a162b" SOURCE /dev/mapper/yoda--pool0-yoda--15199981--7229--45e9--b3a0--b5b30a6a162b]]
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		text := findmntRE.FindAllStringSubmatch(scanner.Text(), -1)
		if len(text) == 2 && len(text[0]) == 3 {
			s.MPDiskMap[text[0][2]] = text[1][2]
		} else {
			return fmt.Errorf("output of findmnt is not correct: %v", text)
		}
	}
	return nil
}

func GetLocalStorageInfo() (*LocalStorageInfo, error) {
	s := &LocalStorageInfo{
		DiskNumberMap:    make(map[string]string),
		NumberDiskMap:    make(map[string]string),
		PartitionDiskMap: make(map[string]string),
		VGDiskMap:        make(map[string]string),
		LVMapperVGMap:    make(map[string]string),
		MPDiskMap:        make(map[string]string),
	}

	if err := s.scanDevices(); err != nil {
		return nil, err
	}
	if err := s.scanVolumeGroups(); err != nil {
		return nil, err
	}
	if err := s.scanLogicalVolumes(); err != nil {
		return nil, err
	}
	if err := s.scanMountPoints(); err != nil {
		return nil, err
	}

	return s, nil
}

func getDiskNumberFromDeviceNumber(number string) string {
	checkExp := regexp.MustCompile(`^\d+:\d+$`)
	if !checkExp.MatchString(number) {
		klog.Warningf("%s is not a device number", number)
		return ""
	}
	partNumberStr := strings.Split(number, ":")
	minNumber, _ := strconv.Atoi(partNumberStr[1])
	return fmt.Sprintf("%s:%d", partNumberStr[0], minNumber-minNumber%16)
}

func getDeviceName(device string) string {
	return fmt.Sprintf(deviceName, device)
}

func getLVMMapperName(vgName, lvName string) string {
	return fmt.Sprintf(lvmMapperName, strings.ReplaceAll(vgName, "-", "--"), strings.ReplaceAll(lvName, "-", "--"))
}
