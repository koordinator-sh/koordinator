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

const (
	NVIDIAVendorID      = "0x10de"
	NVIDIA              = "nvidia"
	HUAWEI              = "huawei"
	HUAWEIVendorID      = "0x19e5"
	HUAWEIRealVendorID  = "19e5"
	HUAWEI910BProductID = "0xd801"
	HUAWEI910ProductID  = "0xd802"
	HUAWEI310PProductID = "0xd500"
	KUNLUNVendorID      = "0x1d22"
	KUNLUNRealVendorID  = "1d22"
	MLUVendorID         = "0xcabc"
	MLURealVendorID     = "cabc"
	IXVendorID          = "0x1e3e"
	GPU                 = "gpu"
	NPU                 = "npu"
	XPU                 = "xpu"
	IX                  = "ix"
	MLU                 = "mlu"

	//The host root directory for the cnotainer
	HostRootDir = "/hostfs"

	ChangeRootCmd = "chroot"

	IXSmiCmd = "ixsmi"

	MLUCmd           = "cnmon"
	QueryMLUUUIDArgs = "-l"
	QueryMLUInfoArgs = "info"
	QueryMLUCard     = "-c"

	NvidiaSmiCmd      = "nvidia-smi"
	QueryGPUListGPU   = "--list-gpus"
	QueryGPUNameArgs  = "--query-gpu=gpu_name"
	QuerGPUFormatArgs = "--format=csv,noheader"
	QueryGPUIdArgs    = "--id=0"
	QueryGPUMemArgs   = "--query-gpu=memory.total"

	NPUSmiCmd             = "npu-smi"
	QueryNPUInfoArgs      = "info"
	QueryNPUMappingeArgs  = "-m"
	QueryNPUTopoArgs      = "-l"
	QueryNPUTypeArgs      = "-t"
	QueryNPUMemTypeArgs   = "memory"
	QueryNPUComTypeArgs   = "common"
	QueryNPUCpuNumCfgArgs = "cpu-num-cfg"
	QueryNPUCardIDArgs    = "-i"
	QueryNPUBoardArgs     = "board"
	QueryTopoArgs         = "topo"

	XPUSmiCmd                  = "xpu_smi"
	QueryXPUMachineReadbleArgs = "-m"
	QueryXPUDeivceIDQueryArgs  = "-d"
)
