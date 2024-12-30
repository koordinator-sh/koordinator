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

package podgpu

import (
	"fmt"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/gpu"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
	"k8s.io/klog/v2"
	pb "k8s.io/kubelet/pkg/apis/podresources/v1alpha1"
)

type podGpuDeletePlugin struct{}

func (p *podGpuDeletePlugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", "nodePodResourcese gpu delete")
	hooks.Register(rmconfig.PreRemoveRunPodSandbox, "nodePodResourcese gpu delete", "delete nodePodResourcese's gpu data when pod which use gpu is deleted", p.DeletePodGPU)
}

var singleton *podGpuDeletePlugin

func Object() *podGpuDeletePlugin {
	if singleton == nil {
		singleton = &podGpuDeletePlugin{}
	}
	return singleton
}

func (p *podGpuDeletePlugin) DeletePodGPU(proto protocol.HooksProtocol) error {
	podCtx := proto.(*protocol.PodContext)
	klog.V(1).Info("DeletePodGPU(): podCtx:%v", podCtx)
	if podCtx == nil {
		return fmt.Errorf("container protocol is nil for plugin gpu")
	}

	klog.V(1).Info("DeletePodGPU(): start to parse params")
	containerReq := podCtx.Request

	podName := containerReq.PodMeta.Name
	nameSpace := containerReq.PodMeta.Namespace
	klog.V(1).Infof("DeletePodGPU(): container %s/%s in nodePodResources", podName, nameSpace)

	gpu.PodResourcesLock.Lock()
	defer gpu.PodResourcesLock.Unlock()

	for i, podResources := range gpu.NodePodResources {
		if podResources.Name == podName && podResources.Namespace == nameSpace {
			klog.V(1).Infof("DeletePodGPU(): found pod %s/%s in nodePodResources", podName, nameSpace)

			if err := removePodResourceByIndex(&gpu.NodePodResources, i); err != nil {
				klog.Errorf("removePodResourceByIndex error:%v", err)
			}
			klog.V(1).Infof("DeletePodGPU(): deleted pod %s/%s from nodePodResources", podName, nameSpace)
			return nil
		}
	}

	klog.V(1).Infof("DeletePodGPU(): not found pod %s/%s in nodePodResources, then no delete podInfo", podName, nameSpace)
	return nil
}

func removePodResourceByIndex(resources *[]*pb.PodResources, index int) error {
	if index < 0 || index >= len(*resources) {
		return fmt.Errorf("invalid index %d", index)
	}

	*resources = append((*resources)[:index], (*resources)[index+1:]...)
	klog.V(1).Infof("removePodResourceByIndex(): Removed pod resource at index %d and newresources is %v", index, resources)
	return nil
}
