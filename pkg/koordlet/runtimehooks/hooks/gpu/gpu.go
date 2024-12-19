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
	"google.golang.org/grpc"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
	pb "k8s.io/kubelet/pkg/apis/podresources/v1alpha1"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

const (
	GpuAllocEnv                         = "NVIDIA_VISIBLE_DEVICES"
	ServerPodResourcesKubeletSocket     = "/pod-resources/koordlet.sock"
	ServerPodResourcesKubeletCheckPoint = "/pod-resources/podgpu.json"
	interval                            = 30 * time.Second
)

var (
	NodePodResources []*pb.PodResources
	PodResourcesLock sync.RWMutex
)

type PodResourcesServer struct{}

func (s *PodResourcesServer) List(ctx context.Context, req *pb.ListPodResourcesRequest) (*pb.ListPodResourcesResponse, error) {
	klog.V(1).Infof("List(): list resp nodePodResources %v", NodePodResources)
	return &pb.ListPodResourcesResponse{PodResources: NodePodResources}, nil
}

type gpuPlugin struct{}

func (p *gpuPlugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", "gpu env inject")
	hooks.Register(rmconfig.PreCreateContainer, "gpu env inject", "inject NVIDIA_VISIBLE_DEVICES env into container", p.InjectContainerGPUEnv)

	//Construct the memory object by loading pod-gpu.json from disk
	nodePodResources, err := LoadNodePodResourcesFromFile(ServerPodResourcesKubeletCheckPoint)
	if err != nil {
		klog.Errorf("Register(): Error loading nodePodResources: %v\n", err)
	}
	NodePodResources = nodePodResources
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
	gpuIDs := []string{}
	for _, d := range devices {
		gpuIDs = append(gpuIDs, fmt.Sprintf("%d", d.Minor))
	}
	if containerCtx.Response.AddContainerEnvs == nil {
		containerCtx.Response.AddContainerEnvs = make(map[string]string)
	}
	containerCtx.Response.AddContainerEnvs[GpuAllocEnv] = strings.Join(gpuIDs, ",")
	if containerReq.PodLabels[ext.LabelGPUIsolationProvider] == string(ext.GPUIsolationProviderHAMICore) {
		gpuResources := devices[0].Resources
		gpuMemoryRatio, ok := gpuResources[ext.ResourceGPUMemoryRatio]
		if !ok {
			return fmt.Errorf("gpu memory ratio not found in gpu resource")
		}
		if gpuMemoryRatio.Value() < 100 {
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
		}
	}

	klog.V(1).Infof("InjectContainerGPUEnv(): start to convertToPodResources")
	convertToPodResources(containerReq, devices)

	return nil
}

func convertToPodResources(request protocol.ContainerRequest, deviceAllocation []*ext.DeviceAllocation) *pb.ContainerDevices {
	klog.V(1).Infof("convertToPodResources(): enter into convertToPodResources")
	podName := request.PodMeta.Name
	nameSpace := request.PodMeta.Namespace
	containerName := request.ContainerMeta.Name

	devices := make([]string, 0, len(deviceAllocation))
	for _, device := range deviceAllocation {
		devices = append(devices, device.ID)
	}

	containerDevices := &pb.ContainerDevices{
		ResourceName: ext.ResourceNvidiaGPU.String(),
		DeviceIds:    devices,
	}
	klog.V(1).Infof("convertToPodResources(): containerDevices: %v", containerDevices)

	containerDevicesSlice := make([]*pb.ContainerDevices, 0, 1)
	containerDevicesSlice = append(containerDevicesSlice, containerDevices)

	var podFound bool

	PodResourcesLock.RLock()
	for i, podResources := range NodePodResources {
		klog.V(1).Infof("convertToPodResources(): traversal nodePodResources %d,%s/%s", i, podResources.GetName(), podResources.GetNamespace())
		if podResources.Name == podName && podResources.Namespace == nameSpace {
			klog.V(1).Infof("convertToPodResources(): pod is found in nodePodResources %d,%s/%s", i, podResources.GetName(), podResources.GetNamespace())
			podFound = true

			PodResourcesLock.RUnlock()

			PodResourcesLock.Lock()
			containerExists := false
			for j, container := range podResources.Containers {
				if container.Name == containerName {
					klog.V(1).Infof("convertToPodResources(): container %s is found", containerName)
					containerExists = true
					podResources.Containers[j] = &pb.ContainerResources{
						Name:    containerName,
						Devices: containerDevicesSlice,
					}
					break
				}
			}
			if !containerExists {
				klog.V(1).Infof("convertToPodResources(): container %s is not found", containerName)
				podResources.Containers = append(podResources.Containers, &pb.ContainerResources{
					Name:    containerName,
					Devices: containerDevicesSlice,
				})
			}
			NodePodResources[i] = podResources
			PodResourcesLock.Unlock()
			break
		}
	}

	if !podFound {
		PodResourcesLock.RUnlock()
		PodResourcesLock.Lock()
		newPodResources := &pb.PodResources{
			Name:      podName,
			Namespace: nameSpace,
			Containers: []*pb.ContainerResources{
				{
					Name:    containerName,
					Devices: containerDevicesSlice,
				},
			},
		}
		NodePodResources = append(NodePodResources, newPodResources)
		PodResourcesLock.Unlock()
	}

	klog.V(1).Infof("convertToPodResources(): nodePodResources: %v", NodePodResources)
	return containerDevices
}

func StartGrpc() error {
	lis, err := net.Listen("unix", ServerPodResourcesKubeletSocket)
	if err != nil {
		klog.Errorf("failed to listen: %v", err)
		return err
	}
	if err := setSocketPermissions(ServerPodResourcesKubeletSocket); err != nil {
		klog.Errorf("failed to set socket permissions: %v", err)
		return err
	}
	server := grpc.NewServer()
	pb.RegisterPodResourcesListerServer(server, &PodResourcesServer{})

	startCheckpoint()

	klog.V(4).Infof("startGrpc():Starting gRPC server on %s", ServerPodResourcesKubeletSocket)
	if err := server.Serve(lis); err != nil {
		klog.Errorf("failed to serve: %v", err)
		return err
	}
	klog.V(1).Infof("startGrpc():end...")
	return nil
}

func setSocketPermissions(socketPath string) error {
	// In a real application, you would set the correct permissions here.
	// For example:
	return os.Chmod(socketPath, 0660)
	//return nil
}

func startCheckpoint() {
	stopCh := make(chan struct{})
	go PeriodicSave(ServerPodResourcesKubeletCheckPoint, interval, stopCh)
}

func EnsureDirectory(path string) error {
	return os.MkdirAll(filepath.Dir(path), os.ModePerm)
}

func SaveNodePodResourcesToFile(filename string, data []*pb.PodResources) error {
	if err := EnsureDirectory(filename); err != nil {
		return fmt.Errorf("failed to ensure directory for %s: %v", filename, err)
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal nodePodResources to JSON: %v", err)
	}

	if err := ioutil.WriteFile(filename, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write JSON data to file %s: %v", filename, err)
	}
	klog.V(5).Infof("SaveNodePodResourcesToFile(): Saved nodePodResources to %s", filename)
	return nil
}

func PeriodicSave(filename string, interval time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := SaveNodePodResourcesToFile(filename, NodePodResources); err != nil {
				klog.V(1).Infof("PeriodicSave(): SaveNodePodResourcesToFile(): Error saving nodePodResources: %v", err)
			}
		case <-stopCh:
			fmt.Println("Stopping periodic save.")
			return
		}
	}
}

func LoadNodePodResourcesFromFile(filePath string) ([]*pb.PodResources, error) {
	klog.V(1).Infof("LoadNodePodResourcesFromFile():start to load PodResources from %s", filePath)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %v", filePath, err)
	}

	var nodePodResources []*pb.PodResources
	if err := json.Unmarshal(data, &nodePodResources); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON data from file %s: %v", filePath, err)
	}

	klog.V(1).Infof("LoadNodePodResourcesFromFile():Loaded %d PodResources from %s\n", len(nodePodResources), filePath)
	return nodePodResources, nil
}
