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

package rdma

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/klog/v2"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

const (
	IBDevDir  = "/dev/infiniband"
	RdmaCmDir = "/dev/infiniband/rdma_cm"
	SysBusPci = "/sys/bus/pci/devices"
)

type rdmaPlugin struct{}

func (p *rdmaPlugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", "rdma device inject")
	hooks.Register(rmconfig.PreCreateContainer, "rdma device inject", "inject NVIDIA_VISIBLE_DEVICES env into container", p.InjectDevice)
}

var singleton *rdmaPlugin

func Object() *rdmaPlugin {
	if singleton == nil {
		singleton = &rdmaPlugin{}
	}
	return singleton
}

func (p *rdmaPlugin) InjectDevice(proto protocol.HooksProtocol) error {
	containerCtx := proto.(*protocol.ContainerContext)
	if containerCtx == nil {
		return fmt.Errorf("container protocol is nil for plugin gpu")
	}
	containerReq := containerCtx.Request
	alloc, err := ext.GetDeviceAllocations(containerReq.PodAnnotations)
	if err != nil {
		klog.Errorf("InjectDevice: GetDeviceAllocations error:%v", err)
		return err
	}
	devices, ok := alloc[schedulingv1alpha1.RDMA]
	if !ok || len(devices) == 0 {
		klog.V(5).Infof("no rdma alloc info in pod anno, %s", containerReq.PodMeta.Name)
		return nil
	}

	deviceInfoCM, err := system.GetDeviceNumbers(RdmaCmDir)
	if err != nil {
		klog.Errorf("InjectDevice: GetDeviceNumbers deviceinfoCM from %s error:%v", RdmaCmDir, err)
		return err
	}

	containerCtx.Response.AddContainerDevices = []*protocol.LinuxDevice{
		{
			Path:          RdmaCmDir,
			Type:          "c",
			Major:         deviceInfoCM[0],
			Minor:         deviceInfoCM[1],
			FileModeValue: 0666,
		},
	}

	for _, device := range devices {
		// Both VF and PF of the same device are not allowed
		if device.Extension != nil && device.Extension.VirtualFunctions != nil && len(device.Extension.VirtualFunctions) > 0 {
			for _, vf := range device.Extension.VirtualFunctions {
				uverbsOfVF, err := getUVerbsViaPciAdd(vf.BusID)
				if err != nil {
					klog.Errorf("InjectDevice: getUVerbsViaPciAdd uverbsOfVF error:%v", err)
					return err
				}

				deviceInfoVf, err := system.GetDeviceNumbers(uverbsOfVF)
				if err != nil {
					klog.Errorf("InjectDevice: GetDeviceNumbers deviceinfoVf from %s error:%v", uverbsOfVF, err)
					return err
				}
				containerCtx.Response.AddContainerDevices = append(containerCtx.Response.AddContainerDevices,
					&protocol.LinuxDevice{
						Path:          uverbsOfVF,
						Major:         deviceInfoVf[0],
						Minor:         deviceInfoVf[1],
						Type:          "c",
						FileModeValue: 0666,
					})
			}
			continue
		}

		uverbs, err := getUVerbsViaPciAdd(device.ID)
		if err != nil {
			klog.Errorf("InjectDevice: getUVerbsViaPciAdd error:%v", err)
			return err
		}
		deviceInfoPf, err := system.GetDeviceNumbers(uverbs)
		if err != nil {
			klog.Errorf("InjectDevice: GetDeviceNumbers deviceinfoPf from %s error:%v", uverbs, err)
			return err
		}
		containerCtx.Response.AddContainerDevices = append(containerCtx.Response.AddContainerDevices,
			&protocol.LinuxDevice{
				Path:          uverbs,
				Major:         deviceInfoPf[0],
				Minor:         deviceInfoPf[1],
				Type:          "c",
				FileModeValue: 0666,
			})
	}
	klog.V(4).Infof("InjectDevice: AddContainerDevices: %v", containerCtx.Response.AddContainerDevices)
	return nil
}

func getUVerbsViaPciAdd(pciAddress string) (string, error) {
	pciDir := filepath.Join(SysBusPci, pciAddress, "infiniband_verbs")
	files, err := os.ReadDir(pciDir)
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("failed to get uverbs: %s", err.Error())
	}
	return filepath.Join(IBDevDir, files[0].Name()), nil
}
