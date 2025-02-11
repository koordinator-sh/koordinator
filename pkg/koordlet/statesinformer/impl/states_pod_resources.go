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

package impl

import (
	"context"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/kubelet"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	podResourcesInformerName PluginName = "podResourcesInformer"
)

var (
	_ podresourcesapi.PodResourcesListerServer = &podResourcesServer{}
)

type podResourcesInformer struct {
	config         *Config
	nodeInformer   *nodeInformer
	resourceServer podresourcesapi.PodResourcesListerServer
}

func newPodResourcesInformer() *podResourcesInformer {
	return &podResourcesInformer{}
}

func (s *podResourcesInformer) Setup(ctx *PluginOption, states *PluginState) {
	s.config = ctx.config

	nodeInformerIf := states.informerPlugins[nodeInformerName]
	nodeInformer, ok := nodeInformerIf.(*nodeInformer)
	if !ok {
		klog.Fatalf("node informer format error")
	}
	s.nodeInformer = nodeInformer
}

func (s *podResourcesInformer) Start(stopCh <-chan struct{}) {
	if !features.DefaultKoordletFeatureGate.Enabled(features.PodResourcesProxy) {
		return
	}
	klog.V(2).Infof("starting pod resources informer")
	if !cache.WaitForCacheSync(stopCh, s.nodeInformer.HasSynced) {
		klog.Fatalf("timed out waiting for node caches to sync")
	}
	stub, err := newKubeletStubFromConfig(s.nodeInformer.GetNode(), s.config)
	if err != nil {
		klog.Fatalf("create kubelet stub, %v", err)
	}
	resourceClient, err := kubelet.GetResourceClient("")
	if err != nil {
		klog.Fatalf("create resource client, %v", err)
	}
	s.resourceServer = &podResourcesServer{
		kubeletStub:       stub,
		podResourceClient: resourceClient,
	}
	err = s.startServer(stopCh)
	if err != nil {
		klog.Fatalf("start grpc server, %v", err)
	}
}

func (s *podResourcesInformer) startServer(stopCh <-chan struct{}) error {
	grpcServerFullSocketPath := filepath.Join(system.Conf.PodResourcesProxyPath, "kubelet.sock")
	err := cleanup(grpcServerFullSocketPath)
	if err != nil {
		klog.Errorf("failed to cleanup %s: %s", grpcServerFullSocketPath, err.Error())
		return err
	}
	sock, err := net.Listen("unix", grpcServerFullSocketPath)
	if err != nil {
		klog.Errorf("failed to listen: %s", err.Error())
		return err
	}
	server := grpc.NewServer()
	podresourcesapi.RegisterPodResourcesListerServer(server, s.resourceServer)
	klog.Infof("Starting GRPC server, grpcServerSocketFullPath: %s", grpcServerFullSocketPath)
	go func() {
		err := server.Serve(sock)
		if err != nil {
			server.Stop()
			klog.Fatalf("pod resources proxy exited with error %s", err.Error())
		}
	}()
	klog.V(2).Infof("pod resources proxy started")
	<-stopCh
	server.GracefulStop()
	return nil
}

func cleanup(grpcServerSocketFullPath string) error {
	if err := os.Remove(grpcServerSocketFullPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *podResourcesInformer) HasSynced() bool {
	return true
}

type podResourcesServer struct {
	podResourceClient podresourcesapi.PodResourcesListerClient
	kubeletStub       KubeletStub
}

func (p *podResourcesServer) List(ctx context.Context, request *podresourcesapi.ListPodResourcesRequest) (*podresourcesapi.ListPodResourcesResponse, error) {
	response, err := p.podResourceClient.List(ctx, request)
	if err != nil {
		return nil, err
	}
	allPods, err := p.kubeletStub.GetAllPods()
	if err != nil {
		return nil, err
	}
	fillPodDevicesAllocatedByKoord(response, &allPods)
	return response, nil
}

func fillPodDevicesAllocatedByKoord(response *podresourcesapi.ListPodResourcesResponse, allPods *corev1.PodList) {
	deviceTypeToResourceName := map[schedulingv1alpha1.DeviceType]string{
		schedulingv1alpha1.GPU:  string(extension.ResourceNvidiaGPU),
		schedulingv1alpha1.RDMA: string(extension.ResourceRDMA),
	}

	podsMap := make(map[string]*corev1.Pod, len(allPods.Items))
	for _, item := range allPods.Items {
		podsMap[util.GetNamespacedName(item.Namespace, item.Name)] = &item
	}

	for _, podResource := range response.PodResources {
		if podResource == nil || len(podResource.Containers) == 0 {
			continue
		}
		pod, ok := podsMap[util.GetNamespacedName(podResource.Namespace, podResource.Name)]
		if !ok {
			continue
		}
		deviceAllocations, err := extension.GetDeviceAllocations(pod.Annotations)
		if err != nil || deviceAllocations == nil {
			continue
		}

		for deviceType, deviceAllocation := range deviceAllocations {
			var deviceIDs []string
			for _, device := range deviceAllocation {
				deviceIDs = append(deviceIDs, device.ID)
			}
			podResource.Containers[0].Devices = append(podResource.Containers[0].Devices, &podresourcesapi.ContainerDevices{
				ResourceName: deviceTypeToResourceName[deviceType],
				DeviceIds:    deviceIDs,
			})
		}
		break
	}
}

func (p *podResourcesServer) GetAllocatableResources(ctx context.Context, request *podresourcesapi.AllocatableResourcesRequest) (*podresourcesapi.AllocatableResourcesResponse, error) {
	response, err := p.podResourceClient.GetAllocatableResources(ctx, request)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (p *podResourcesServer) Get(ctx context.Context, request *podresourcesapi.GetPodResourcesRequest) (*podresourcesapi.GetPodResourcesResponse, error) {
	response, err := p.podResourceClient.Get(ctx, request)
	if err != nil {
		return nil, err
	}
	return response, err
}
