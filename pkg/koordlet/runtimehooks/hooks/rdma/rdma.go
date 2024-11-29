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

	"github.com/containerd/nri/pkg/api"
	"k8s.io/klog/v2"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

const (
	IBDevDir  = "/dev/infiniband"
	RDMACM    = "/dev/infiniband/rdma_cm"
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
		return err
	}
	devices, ok := alloc[schedulingv1alpha1.RDMA]
	if !ok || len(devices) == 0 {
		klog.V(5).Infof("no rdma alloc info in pod anno, %s", containerReq.PodMeta.Name)
		return nil
	}
	containerCtx.Response.AddContainerDevices = []*api.LinuxDevice{
		{
			Path: RDMACM,
			Type: "c",
			FileMode: &api.OptionalFileMode{
				Value: 0666,
			},
		},
	}

	for _, device := range devices {
		for _, vf := range device.Extension.VirtualFunctions {
			uverbsOfVF, err := getUVerbsViaPciAdd(vf.BusID)
			if err != nil {
				return err
			}
			containerCtx.Response.AddContainerDevices = append(containerCtx.Response.AddContainerDevices,
				&api.LinuxDevice{
					Path: uverbsOfVF,
					Type: "c",
					FileMode: &api.OptionalFileMode{
						Value: 0666,
					},
				})
		}
		uverbs, err := getUVerbsViaPciAdd(device.ID)
		if err != nil {
			return err
		}
		containerCtx.Response.AddContainerDevices = append(containerCtx.Response.AddContainerDevices,
			&api.LinuxDevice{
				Path: uverbs,
				Type: "c",
				FileMode: &api.OptionalFileMode{
					Value: 0666,
				},
			})
	}
	return nil
}

func getUVerbsViaPciAdd(pciAddress string) (string, error) {
	pciDir := filepath.Join(SysBusPci, pciAddress, "infiniband_verbs")
	files, err := os.ReadDir(pciDir)
	if err != nil || len(files) == 0 {
		klog.Errorf("fail read pciDir %v", err)
		return "", fmt.Errorf("failed to get uverbs: %s", err.Error())
	}
	return filepath.Join(IBDevDir, files[0].Name()), nil
}
