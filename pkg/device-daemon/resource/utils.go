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

package resource

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jaypipes/ghw/pkg/pci"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/devices/helper"
	koordletuti "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

var (
	pcieRegexp = regexp.MustCompile(`pci\d{4}:[0-9a-fA-F]{2}`)
	devRegexp  = regexp.MustCompile(`^/dev/davinci(\d+)$`)
)

// IsNPUDevice only consider 910B/910B3/310P3
func IsNPUDevice(device *pci.Device) bool {
	klog.V(3).Info("[IsNPUDevice] device.Vendor.ID: ", device.Vendor.ID)
	return device.Vendor.ID == HUAWEIVendorID || device.Vendor.ID == HUAWEIRealVendorID
}

func IsIXDevice(device *pci.Device) bool {
	return device.Vendor.ID == IXVendorID
}

func IsXPUDevice(device *pci.Device) bool {
	klog.V(3).Info("[IsXPUDevice] device.Vendor.ID: ", device.Vendor.ID)
	return device.Vendor.ID == KUNLUNVendorID || device.Vendor.ID == KUNLUNRealVendorID
}

func IsMLUDevice(device *pci.Device) bool {
	klog.V(4).Info("[IsMLUDevice] device.Vendor.ID: ", device.Vendor.ID)
	return device.Vendor.ID == MLUVendorID || device.Vendor.ID == MLURealVendorID
}

// GetXPUName exec cmd in chroot to get xpu name
func GetXPUName(deviceType string) (string, error) {
	switch deviceType {
	case GPU:
		klog.Info("GetGPUName, start to get gpu name")

		cmd := exec.Command(ChangeRootCmd, HostRootDir, NvidiaSmiCmd, QueryGPUNameArgs, QuerGPUFormatArgs, QueryGPUIdArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("get gpu name err: %w, output %v", err, string(output))
		}

		results := strings.ReplaceAll(string(output), " ", "-")
		klog.Infof("GetXPUName gpu name: %s", results)

		return strings.TrimSpace(results), nil
	case NPU:
		klog.Info("GetNPUName, start to get npu name")
		// npu-smi info -m
		cmd := exec.Command(ChangeRootCmd, HostRootDir, NPUSmiCmd, QueryNPUInfoArgs, QueryNPUMappingeArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("get npu name err: %w, output %v", err, string(output))
		}
		klog.Infof("GetNPUName results: %s", string(output))

		lines := strings.Split(string(output), "\n")
		if len(lines) == 0 {
			return "", fmt.Errorf("get npu name err, no npu name line")
		}
		fields := strings.Fields(lines[1])
		// keep the format consistent with NVIDIA
		results := fmt.Sprintf("%s-%s", fields[len(fields)-2], fields[len(fields)-1])
		return results, nil
	case IX:
		// ixsmi --query-gpu=gpu_name --format=csv,noheader --id=0 | sed -e 's/ /-/g'
		klog.Info("GetIXName, start to get ix name")
		cmd := exec.Command(ChangeRootCmd, HostRootDir, IXSmiCmd, QueryGPUNameArgs, QuerGPUFormatArgs, QueryGPUIdArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("get ix name err: %w, output %v", err, string(output))
		}

		results := strings.ReplaceAll(string(output), " ", "-")
		klog.Infof("GetXPUName ix name: %s", results)

		return strings.TrimSpace(results), nil
	case XPU:
		klog.Info("GetXPUName, start to get xpu name")
		cmd := exec.Command(ChangeRootCmd, HostRootDir, XPUSmiCmd, QueryXPUMachineReadbleArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("get xpu name err: %w, output %v", err, string(output))
		}
		klog.V(3).Info("GetXPUName xpu name:", string(output))
		lines := strings.Split(string(output), "\n")
		fields := strings.Fields(lines[0])
		if len(fields) < 22 {
			return "", fmt.Errorf("get xpu name err, no xpu name field")
		}
		return fields[21], nil

	case MLU:
		klog.Info("GetMLUName, start to get MLU name")
		cmd := exec.Command(ChangeRootCmd, HostRootDir, MLUCmd, QueryMLUUUIDArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("get MLU name err: %w, output %v", err, string(output))
		}
		fields := strings.Fields(string(output))
		for str := range fields {
			if strings.Contains(fields[str], "MLU") {
				klog.Infof("GetMLUName results: %s", fields[str])
				return fields[str], nil
			}
		}
		return "", fmt.Errorf("get MLU name err, no MLU name found in output: %s", string(output))
	default:
		return "", fmt.Errorf("get xpu name err, unsupported device type: %s", deviceType)
	}
}

// GetXPUCount exec cmd in chroot to get xpu count
func GetXPUCount(deviceType string) (int, error) {
	switch deviceType {
	case GPU:
		klog.Info("GetGPUCount, start to get gpu count")

		// nvidia-smi --list-gpus
		cmd := exec.Command(ChangeRootCmd, HostRootDir, NvidiaSmiCmd, QueryGPUListGPU)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("get gpu count err: %w, output %v", err, string(output))
		}

		results := strings.Split(string(output), "\n")
		klog.Infof("GetGPUCount, end to get gpu count: %s", results)

		count := len(results) - 1
		if count < 0 {
			count = 0
		}

		return count, nil
	case NPU:
		klog.Info("GetNPUCount, start to get npu count")

		// npu-smi info -l
		cmd := exec.Command(ChangeRootCmd, HostRootDir, NPUSmiCmd, QueryNPUInfoArgs, QueryNPUTopoArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("get npu count err: %w, output %v", err, string(output))
		}
		klog.Infof("GetNPUCount, end to get npu count: %s", string(output))

		var totalCountLine string
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.Contains(line, "Total Count") {
				totalCountLine = strings.TrimSpace(line)
				break
			}
		}
		if totalCountLine == "" {
			return 0, fmt.Errorf("get npu count err, no total count line")
		}
		fidlds := strings.Fields(totalCountLine)
		if len(fidlds) < 4 {
			return 0, fmt.Errorf("get npu count err, no total count field")
		}
		count, err := strconv.Atoi(fidlds[4])
		if err != nil {
			return 0, fmt.Errorf("get npu count err, failed to convert field to integer, %w", err)
		}
		return count, nil
	case XPU:
		klog.Info("GetXPUCount, start to get xpu count")
		cmd := exec.Command("sh", "-c", fmt.Sprintf("%s %s %s %s | wc -l", ChangeRootCmd, HostRootDir, XPUSmiCmd, QueryXPUMachineReadbleArgs))
		output, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("get xpu count err: %w, output %v", err, string(output))
		}
		klog.Infof("GetXPUCount, end to get xpu count: %s", string(output))
		results := strings.Split(string(output), "\n")
		count, err := strconv.Atoi(results[0])
		if err != nil {
			return 0, fmt.Errorf("get xpu count err, failed to convert field to integer, %w", err)
		}
		return count, nil
	case IX:
		klog.Info("GetIXCount, start to get ix count")
		// ixsmi -L | wc -l
		cmd := exec.Command(ChangeRootCmd, HostRootDir, IXSmiCmd, QueryGPUListGPU)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("get ix count err: %w, output %v", err, string(output))
		}

		results := strings.Split(string(output), "\n")
		klog.Infof("GetIXCount, end to get ix count: %s", results)

		count := len(results) - 1
		if count < 0 {
			count = 0
		}

		return count, nil
	case MLU:
		klog.Info("GetMLUCount, start to get mlu count")
		// cnmon -l | grep UUID | wc -l
		cmd := exec.Command("sh", "-c", fmt.Sprintf("%s %s %s %s | grep \"UUID\" | wc -l",
			ChangeRootCmd, HostRootDir, MLUCmd, QueryMLUUUIDArgs))
		output, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("get mlu count err: %w, output %v", err, string(output))
		}

		countStr := strings.TrimSpace(string(output))
		count, err := strconv.Atoi(countStr)
		if err != nil {
			return 0, fmt.Errorf("failed to parse mlu count: %v", err)
		}

		klog.Infof("GetMLUCount, end to get mlu count: %d", count)
		return count, nil

	default:
		return 0, fmt.Errorf("get xpu count err, unsupported device type: %s", deviceType)
	}
}

// GetXPUMemory exec cmd in chroot to get xpu memory, return Mi(K8s resource unit)
func GetXPUMemory(deviceType string) (string, error) {
	switch deviceType {
	case GPU:
		klog.Info("GetGPUMemory, start to get gpu memory")

		// nvidia-smi --query-gpu=memory.total --format=csv,noheader --id=0
		cmd := exec.Command(ChangeRootCmd, HostRootDir, NvidiaSmiCmd, QueryGPUMemArgs, QuerGPUFormatArgs, QueryGPUIdArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "0Mi", fmt.Errorf("get gpu memory err: %w, output %v", err, string(output))
		}

		results := strings.TrimSpace(string(output))
		results = strings.ReplaceAll(results, " ", "")
		klog.Infof("GetGPUMemory, end to get gpu memory: %s", results)

		results = strings.ReplaceAll(results, "MiB", "Mi")

		return fmt.Sprintf("%s", results), nil
	case NPU:
		klog.Info("GetNPUMemory, start to get npu memory")

		// npu-smi -t memory -i 1
		cmd := exec.Command(ChangeRootCmd, HostRootDir, NPUSmiCmd, QueryNPUTypeArgs, QueryNPUMemTypeArgs, QueryNPUCardIDArgs, "1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			if strings.Contains(string(output), "Command or option is not found") {
				// for 310P3
				return "21527Mi", nil
			}
			return "0Mi", fmt.Errorf("get npu memory err: %w, output %v", err, string(output))
		}
		klog.Infof("GetNPUMemory, end to get npu memory: %s", string(output))

		lines := strings.Split(string(output), "\n")
		if len(lines) == 0 {
			return "0Mi", fmt.Errorf("get npu memory err, no npu memory line")
		}
		var memCount int
		for _, line := range lines {
			if strings.Contains(line, "DDR Capacity") || strings.Contains(line, "HBM Capacity") {
				fields := strings.Fields(line)
				countStr := fields[len(fields)-1]
				countInt, err := strconv.Atoi(countStr)
				if err != nil {
					return "0Mi", fmt.Errorf("get npu memory err, failed to convert field to integer, %w", err)
				}
				memCount += countInt
			}
		}
		return fmt.Sprintf("%dMi", memCount), nil
	case IX:
		// ixsmi --id=0 --query-gpu=memory.total --format=csv,noheader | sed -e 's/ //g'
		klog.Info("GetIXMemory, start to get ix memory")
		cmd := exec.Command(ChangeRootCmd, HostRootDir, IXSmiCmd, QueryGPUMemArgs, QuerGPUFormatArgs, QueryGPUIdArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "0Mi", fmt.Errorf("get gpu memory err: %w, output %v", err, string(output))
		}

		results := strings.TrimSpace(string(output))
		results = strings.ReplaceAll(results, " ", "")
		klog.Infof("GetGPUMemory, end to get ix memory: %s", results)

		results = strings.ReplaceAll(results, "MiB", "Mi")

		return fmt.Sprintf("%s", results), nil
	case XPU:
		klog.Info("GetXPUMemory, start to get xpu memory")
		cmd := exec.Command(ChangeRootCmd, HostRootDir, XPUSmiCmd, QueryXPUMachineReadbleArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "0Mi", fmt.Errorf("get xpu memory err: %w, output %v", err, string(output))
		}
		klog.Infof("GetXPUMemory, end to get xpu memory: %s", string(output))
		results := strings.Split(string(output), "\n")
		fields := strings.Fields(results[0])
		if len(fields) < 19 {
			return "0Mi", fmt.Errorf("get xpu memory err, no xpu memory field")
		}
		return fields[18] + "Mi", nil
	case MLU:
		klog.Info("GetMLUMemory, start to get mlu memory")
		cmd := exec.Command("sh", "-c", fmt.Sprintf("%s %s %s %s | grep \"Physical Memory Usage\" -A 1",
			ChangeRootCmd, HostRootDir, MLUCmd, QueryMLUInfoArgs))
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "0Mi", fmt.Errorf("get gpu memory err: %w, output %v", err, string(output))
		}

		results := strings.Split(string(output), "\n")
		var realResults string
		for _, line := range results {
			if strings.Contains(line, "Total") {
				fields := strings.Fields(line)
				realResults = fields[len(fields)-2]
				realResults = strings.TrimSpace(realResults)
				break
			}
		}
		klog.Infof("GetGPUMemory, end to get ix memory: %s", realResults)

		realResults = fmt.Sprintf("%sMi", realResults)

		return fmt.Sprintf("%s", realResults), nil
	default:
		return "0Mi", fmt.Errorf("get xpu memory err, unsupported device type: %s", deviceType)
	}
}

func GetXPUCards(deviceType string) ([]string, error) {
	switch deviceType {
	case NPU:
		klog.Info("GetXPUCards, start to get npu cards")
		// npu-smi info -l
		cmd := exec.Command(ChangeRootCmd, HostRootDir, NPUSmiCmd, QueryNPUInfoArgs, QueryNPUTopoArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			klog.Error("GetXPUCards, failed to get npu minors: ", err)
			return []string{}, fmt.Errorf("get npu card err: %w, output %v", err, string(output))
		}
		stringlines := strings.Split(string(output), "\n")
		var minors []string
		for _, line := range stringlines {
			if strings.Contains(line, "NPU ID") {
				fields := strings.Fields(line)
				minor := fields[len(fields)-1]
				klog.Infof("GetXPUCards, get npu card: %v", minor)
				minors = append(minors, minor)
			}
		}
		return minors, nil
	default:
		return []string{}, fmt.Errorf("GetXPUCards, unsupported device type: %s", deviceType)
	}
}

func GetXPUMinors(deviceType string) ([]string, error) {
	switch deviceType {
	case NPU:
		klog.Info("GetXPUMinors, start to get npu minors")
		// ls /dev/davinci*
		matchDevs, err := filepath.Glob("/dev/davinci*")
		if err != nil {
			klog.Errorf("GetXPUMinors, failed to glob davinci devices: %v", err)
			return []string{}, fmt.Errorf("glob davinci devices err: %v", err)
		}

		var minors []string
		for _, dev := range matchDevs {
			klog.Infof("GetXPUMinors, found davinci device: %v", dev)
			sub := devRegexp.FindStringSubmatch(dev)
			if len(sub) < 2 {
				klog.Errorf("GetXPUMinors, failed to match device regex, dev %s", dev)
				continue
			}
			minors = append(minors, sub[1])
		}

		sort.Slice(minors, func(i, j int) bool {
			return minors[i] < minors[j]
		})

		return minors, nil
	default:
		return []string{}, fmt.Errorf("GetXPUMinors, unsupported device type: %s", deviceType)
	}
}

// GetAiCore exec cmd in chroot to get ai core count from npu
func GetAiCore() (int, error) {
	klog.Info("GetAiCore, start to get ai core")

	// npu-smi info -t common -i 0
	cmd := exec.Command(ChangeRootCmd, HostRootDir, NPUSmiCmd, QueryNPUInfoArgs, QueryNPUTypeArgs, QueryNPUComTypeArgs, QueryNPUCardIDArgs, "0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("get ai core err: %w, output %v", err, string(output))
	}
	klog.Infof("GetAiCore, end to get ai core: %s", string(output))

	lines := strings.Split(string(output), "\n")
	if len(lines) == 0 {
		return 0, fmt.Errorf("get ai core err, no ai core line")
	}
	var coreCount int
	for _, line := range lines {
		if strings.Contains(line, "Aicore Count") {
			fields := strings.Fields(line)
			countStr := fields[len(fields)-1]
			countInt, err := strconv.Atoi(countStr)
			if err != nil {
				return 0, fmt.Errorf("get ai core err, failed to convert field to integer, %w", err)
			}
			coreCount += countInt
		}
	}
	return coreCount, nil
}

// GetAiCpu exec cmd in chroot to get ai cpu count from npu
func GetAiCpu(npuID string) (int, error) {
	klog.Info("GetAiCpuCore, start to get ai cpu core")

	// npu-smi info -t cpu-num-cfg -i 1
	cmd := exec.Command(ChangeRootCmd, HostRootDir, NPUSmiCmd, QueryNPUInfoArgs, QueryNPUTypeArgs, QueryNPUCpuNumCfgArgs, QueryNPUCardIDArgs, npuID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("get ai cpu core err: %w, output %v", err, string(output))
	}
	klog.Infof("GetAiCpu, end to get ai cpu: %s", string(output))

	lines := strings.Split(string(output), "\n")
	if len(lines) == 0 {
		return 0, fmt.Errorf("get ai cpu core err, no ai cpu core line")
	}
	var cpuCount int
	for _, line := range lines {
		if strings.Contains(line, "AI") {
			fields := strings.Fields(line)
			countStr := fields[len(fields)-1]
			countInt, err := strconv.Atoi(countStr)
			if err != nil {
				return 0, fmt.Errorf("get ai cpu err, failed to convert field to integer, %w", err)
			}
			cpuCount += countInt
		}
	}
	return cpuCount, nil
}

func GetDeviceInfo(deviceType, index string) (koordletuti.DeviceTopology, koordletuti.DeviceStatus, string, error) {
	switch deviceType {
	case MLU:
		//TODO jess add PCIE switch
		klog.Info("GetDeviceInfo, start to get mlu device info")
		cmd := exec.Command(ChangeRootCmd, HostRootDir, MLUCmd, QueryMLUInfoArgs, QueryMLUCard, index)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return koordletuti.DeviceTopology{}, koordletuti.DeviceStatus{}, " ", fmt.Errorf("get mlu device info err: %w, output %v", err, string(output))
		}
		klog.V(4).Infof("GetDeviceInfo, end to get mlu device info: %s", string(output))
		topo := koordletuti.DeviceTopology{
			SocketID: "-1",
		}
		status := koordletuti.DeviceStatus{}
		lines := strings.Split(string(output), "\n")
		var uuid, domainID, busNum, device, function string
		for _, line := range lines {
			if strings.Contains(line, "UUID") {
				fields := strings.Fields(line)
				if len(fields) < 3 {
					return koordletuti.DeviceTopology{}, status, " ", fmt.Errorf("get mlu device info err, no uuid field")
				}
				uuid = fields[2]

			}
			if strings.Contains(line, "NUMA node id") {
				fields := strings.Fields(line)
				if len(fields) < 3 {
					return koordletuti.DeviceTopology{}, status, " ", fmt.Errorf("get mlu device info err, no numa node id field")
				}
				numaNodeID := fields[len(fields)-1]
				topo.NodeID = numaNodeID
			}
			if strings.Contains(line, "Domain ID") {
				fields := strings.Fields(line)
				if len(fields) < 3 {
					return koordletuti.DeviceTopology{}, status, " ", fmt.Errorf("get mlu device info err, no domain id field")
				}
				domainID = fields[len(fields)-1]
			}
			if strings.Contains(line, "Bus num") {
				fields := strings.Fields(line)
				if len(fields) < 3 {
					return koordletuti.DeviceTopology{}, status, " ", fmt.Errorf("get mlu device info err, no bus num field")
				}
				busNum = fields[len(fields)-1]
			}
			if strings.Contains(line, "Device") {
				klog.V(4).Infof("GetDeviceInfo, get device info by deviceInfo: %s", line)
				trimmed := strings.TrimSpace(line)
				// 使用正则表达式精确匹配 "Device : ..."
				// 支持 Device 后面有任意空格，冒号前后有任意空格
				re := regexp.MustCompile(`^Device\s*:\s*(.+)$`)
				matches := re.FindStringSubmatch(trimmed)

				if len(matches) >= 2 {
					value := strings.TrimSpace(matches[1])
					if value != "" {
						if isNumericRegex(value) {
							// 如果是数字
							device = value
						} else if value == "Good" {
							status.Healthy = true
						} else {
							status.Healthy = false
							status.ErrMessage = fmt.Sprintf("Device is unhealthy, status: %s", value)
						}
					}
				}
			}
			if strings.Contains(line, "Function") {
				fields := strings.Fields(line)
				if len(fields) < 3 {
					return koordletuti.DeviceTopology{}, status, " ", fmt.Errorf("get mlu device info err, no function field")
				}
				function = fields[len(fields)-1]
			}
			if strings.Contains(line, "Dev") {

			}
		}
		topo.BusID = fmt.Sprintf("%s:%s:%s.%s", domainID, busNum, device, function)
		return topo, status, uuid, nil
	case NPU:
		// TODO jess add PCIE switch
		klog.Info("GetDeviceInfo, start to get npu device info")
		klog.Infof("GetDeviceInfo, start to exec npu-smi info -t board -i %s", index)
		// npu-smi info -t board -i 1
		cmd := exec.Command(ChangeRootCmd, HostRootDir, NPUSmiCmd, QueryNPUInfoArgs, QueryNPUTypeArgs, QueryNPUBoardArgs, QueryNPUCardIDArgs, index)
		output, err := cmd.CombinedOutput()
		if err != nil {
			klog.Errorf("GetDeviceInfo, failed to get npu device info: %v, ouput: %s", err, string(output))
			return koordletuti.DeviceTopology{}, koordletuti.DeviceStatus{}, " ", fmt.Errorf("get npu device info err: %w, output %v", err, string(output))
		}
		klog.V(4).Infof("GetDeviceInfo, end to get npu busID: %s", string(output))
		var uuid, busID string
		// Placeholder UUID, replace with actual if available
		uuid = fmt.Sprintf("npu-%s", index)
		status := koordletuti.DeviceStatus{
			Healthy: true,
		}
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			klog.V(4).Infof("GetDeviceInfo, get npu device info by deviceInfo: %s", line)
			if strings.Contains(line, "PCIe Bus Info") {
				fields := strings.Fields(line)
				busID = fields[len(fields)-1]
			}
			if strings.Contains(line, "Chip Fault") {
				fields := strings.Fields(line)
				faultCountStr := fields[len(fields)-1]
				faultCount, _ := strconv.Atoi(faultCountStr)
				if faultCount > 0 {
					status.Healthy = false
					status.ErrMessage = fmt.Sprintf("NPU index %s has %d chip faults", index, faultCount)
					status.ErrCode = fmt.Sprintf("chip error")
				}
			}
		}

		// npu-smi info -t topo -i 1
		cmd = exec.Command(ChangeRootCmd, HostRootDir, NPUSmiCmd, QueryNPUInfoArgs, QueryNPUTypeArgs, QueryTopoArgs, QueryNPUCardIDArgs, index)
		output, err = cmd.CombinedOutput()
		if err != nil {
			klog.Errorf("GetDeviceInfo, failed to get npu topo: %v, ouput: %s", err, string(output))
		}

		model, _ := GetXPUName(NPU)
		if model == "Ascend-310P3" || strings.Contains(string(output), "not support") {
			klog.V(4).Infof("GetDeviceInfo, end to get npu topo: %s", string(output))
		} else {
			// TODO for 910B need parse info
			klog.V(4).Infof("GetDeviceInfo, end to get npu topo: %s", string(output))
		}

		nodeID, pcie, busID, err := helper.ParsePCIInfo(busID)
		if err != nil {
			klog.Errorf("GetDeviceInfo, parse npu  pci info error, %v", err)
		}
		topo := koordletuti.DeviceTopology{
			SocketID: "-1",
			BusID:    busID,
			PCIEID:   pcie,
			NodeID:   fmt.Sprintf("%d", nodeID),
		}

		return topo, status, uuid, nil
	case XPU:
		klog.Info("GetDeviceInfo, start to get xpu device info")
		uuid := fmt.Sprintf("xpu-%s", index)
		// xpu_smi -m -d /dev/xpu*
		cmd := exec.Command(ChangeRootCmd, HostRootDir, XPUSmiCmd, QueryXPUMachineReadbleArgs, QueryXPUDeivceIDQueryArgs, fmt.Sprintf("/dev/xpu%s", index))
		output, err := cmd.CombinedOutput()
		if err != nil {
			return koordletuti.DeviceTopology{}, koordletuti.DeviceStatus{}, " ", fmt.Errorf("get xpu device info err: %w, output %v", err, string(output))
		}
		klog.V(4).Infof("GetDeviceInfo, end to get xpu device info: %s", string(output))

		lines := strings.Split(string(output), "\n")
		fields := strings.Fields(lines[0])
		busID := fields[0]
		nodeID, pcie, busID, err := helper.ParsePCIInfo(busID)
		if err != nil {
			klog.Errorf("GetDeviceInfo, parse xpu device info error, %v", err)
		}
		topo := koordletuti.DeviceTopology{
			SocketID: "-1",
			BusID:    busID,
			PCIEID:   pcie,
			NodeID:   fmt.Sprintf("%d", nodeID),
		}
		deviceStatus := koordletuti.DeviceStatus{
			Healthy: true,
		}
		return topo, deviceStatus, uuid, nil
	default:
		return koordletuti.DeviceTopology{}, koordletuti.DeviceStatus{}, " ", nil
	}
}

func isNumericRegex(s string) bool {
	matched, _ := regexp.MatchString(`^\d+$`, s)
	return matched
}
