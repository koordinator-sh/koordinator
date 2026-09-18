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

package gpu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/klog/v2"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

const (
	GpuAllocEnv = "NVIDIA_VISIBLE_DEVICES"

	nvidiaDevicePrefix   = "/dev/nvidia"
	nvidiaCtlDevice      = "/dev/nvidiactl"
	nvidiaUVMDevice      = "/dev/nvidia-uvm"
	nvidiaUVMToolsDevice = "/dev/nvidia-uvm-tools"
)

// nvidiaControlDevices are common NVIDIA control devices shared by all GPU containers.
var nvidiaControlDevices = []string{nvidiaCtlDevice, nvidiaUVMDevice, nvidiaUVMToolsDevice}

// getDeviceNumbers is a function variable for getting device major/minor numbers,
// allowing substitution in tests.
var getDeviceNumbers = system.GetDeviceNumbers

type gpuPlugin struct{}

func (p *gpuPlugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", "gpu env inject")
	hooks.Register(rmconfig.PreCreateContainer, "gpu env inject", "inject NVIDIA_VISIBLE_DEVICES env into container", p.InjectContainerGPUEnv)
}

var singleton *gpuPlugin

func Object() *gpuPlugin {
	if singleton == nil {
		singleton = &gpuPlugin{}
	}
	return singleton
}

func (p *gpuPlugin) InjectContainerGPUEnv(proto protocol.HooksProtocol) error {
	containerCtx := proto.(*protocol.ContainerContext)
	if containerCtx == nil {
		return fmt.Errorf("container protocol is nil for plugin gpu")
	}
	containerReq := containerCtx.Request
	alloc, err := ext.GetDeviceAllocations(containerReq.PodAnnotations)
	if err != nil {
		return err
	}
	devices, ok := alloc[schedulingv1alpha1.GPU]
	if !ok || len(devices) == 0 {
		klog.V(5).Infof("no gpu alloc info in pod anno, %s", containerReq.PodMeta.Name)
		return nil
	}

	if containerCtx.Response.AddContainerEnvs == nil {
		containerCtx.Response.AddContainerEnvs = make(map[string]string)
	}

	injectGPUEnv(containerCtx, devices)
	if err := injectGPUDevices(containerCtx, devices); err != nil {
		return err
	}
	if containerReq.PodLabels[ext.LabelGPUIsolationProvider] == string(ext.GPUIsolationProviderHAMICore) {
		if err := injectHAMiResources(containerCtx, devices); err != nil {
			return err
		}
	}
	return nil
}

// injectGPUEnv sets the NVIDIA_VISIBLE_DEVICES environment variable with allocated GPU minor numbers.
func injectGPUEnv(containerCtx *protocol.ContainerContext, devices []*ext.DeviceAllocation) {
	gpuIDs := make([]string, 0, len(devices))
	for _, d := range devices {
		gpuIDs = append(gpuIDs, fmt.Sprintf("%d", d.Minor))
	}
	containerCtx.Response.AddContainerEnvs[GpuAllocEnv] = strings.Join(gpuIDs, ",")
}

// injectGPUDevices adds GPU device information to the OCI spec so that runc can persist device
// cgroup rules in systemd transient units. Without this, a `systemctl daemon-reload` would reset
// the device cgroup and cause GPU permission loss for containers using the nvidia-container-runtime
// legacy mode.
func injectGPUDevices(containerCtx *protocol.ContainerContext, devices []*ext.DeviceAllocation) error {
	var gpuDevices []*protocol.LinuxDevice

	for _, d := range devices {
		devicePath := fmt.Sprintf("%s%d", nvidiaDevicePrefix, d.Minor)
		deviceInfo, err := getDeviceNumbers(devicePath)
		if err != nil {
			klog.Warningf("injectGPUDevices: GetDeviceNumbers from %s error: %v, skip device injection", devicePath, err)
			return nil
		}
		gpuDevices = append(gpuDevices, &protocol.LinuxDevice{
			Path:          devicePath,
			Type:          "c",
			Major:         deviceInfo[0],
			Minor:         deviceInfo[1],
			FileModeValue: 0666,
		})
	}

	for _, ctlDevice := range nvidiaControlDevices {
		deviceInfo, err := getDeviceNumbers(ctlDevice)
		if err != nil {
			klog.V(5).Infof("injectGPUDevices: skip control device %s, GetDeviceNumbers error: %v", ctlDevice, err)
			continue
		}
		gpuDevices = append(gpuDevices, &protocol.LinuxDevice{
			Path:          ctlDevice,
			Type:          "c",
			Major:         deviceInfo[0],
			Minor:         deviceInfo[1],
			FileModeValue: 0666,
		})
	}

	containerCtx.Response.AddContainerDevices = append(containerCtx.Response.AddContainerDevices, gpuDevices...)
	klog.V(4).Infof("injectGPUDevices: AddContainerDevices: %v", containerCtx.Response.AddContainerDevices)
	return nil
}

// injectHAMiResources injects HAMi-specific environment variables and mounts for GPU memory/SM isolation.
func injectHAMiResources(containerCtx *protocol.ContainerContext, devices []*ext.DeviceAllocation) error {
	gpuResources := devices[0].Resources
	gpuMemoryRatio, ok := gpuResources[ext.ResourceGPUMemoryRatio]
	if !ok {
		return fmt.Errorf("gpu memory ratio not found in gpu resource")
	}
	if gpuMemoryRatio.Value() >= 100 {
		return nil
	}

	gpuMemory, ok := gpuResources[ext.ResourceGPUMemory]
	if !ok {
		return fmt.Errorf("gpu memory not found in gpu resource")
	}
	containerCtx.Response.AddContainerEnvs["CUDA_DEVICE_MEMORY_LIMIT"] = fmt.Sprintf("%d", gpuMemory.Value())
	gpuCore, ok := gpuResources[ext.ResourceGPUCore]
	if ok {
		containerCtx.Response.AddContainerEnvs["CUDA_DEVICE_SM_LIMIT"] = fmt.Sprintf("%d", gpuCore.Value())
	}
	containerCtx.Response.AddContainerEnvs["LD_PRELOAD"] = system.Conf.HAMICoreLibraryDirectoryPath

	containerCtx.Response.AddContainerMounts = append(containerCtx.Response.AddContainerMounts,
		&protocol.Mount{
			Destination: system.Conf.HAMICoreLibraryDirectoryPath,
			Type:        "bind",
			Source:      system.Conf.HAMICoreLibraryDirectoryPath,
			Options:     []string{"rbind"},
		},
		// Because https://github.com/Project-HAMi/HAMi/issues/696, we create the directory in pod.
		&protocol.Mount{
			Destination: "/tmp/vgpulock",
			Type:        "bind",
			Source:      "/tmp/vgpulock",
			Options:     []string{"rbind"},
		},
	)

	if features.DefaultKoordletFeatureGate.Enabled(features.HamiCoreVGPUMonitor) {
		containerReq := containerCtx.Request
		hamiDirPath := filepath.Dir(system.Conf.HAMICoreLibraryDirectoryPath)
		containerCtx.Response.AddContainerEnvs["CUDA_DEVICE_MEMORY_SHARED_CACHE"] = fmt.Sprintf("%s/%s_%s.cache", hamiDirPath, containerReq.PodMeta.UID, containerReq.ContainerMeta.Name)
		cacheFileHostDirectory := fmt.Sprintf("%s/containers/%s_%s", hamiDirPath, containerReq.PodMeta.UID, containerReq.ContainerMeta.Name)
		// TODO: Move this operation into the pkg resource-executor.
		klog.V(5).Infof("create a vgpu monitoring data directory [%s] and grant it 0777 permissions", cacheFileHostDirectory)
		os.RemoveAll(cacheFileHostDirectory)
		os.MkdirAll(cacheFileHostDirectory, 0777)
		os.Chmod(cacheFileHostDirectory, 0777)
		containerCtx.Response.AddContainerMounts = append(containerCtx.Response.AddContainerMounts,
			&protocol.Mount{
				Destination: hamiDirPath,
				Type:        "bind",
				Source:      cacheFileHostDirectory,
				Options:     []string{"rbind"},
			},
		)
	}
	return nil
}
