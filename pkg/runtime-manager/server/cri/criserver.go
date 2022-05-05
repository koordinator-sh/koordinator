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

package cri

import (
	"context"
	"net"
	"time"

	"google.golang.org/grpc"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/runtime-manager/config"
	"github.com/koordinator-sh/koordinator/pkg/runtime-manager/dispatcher"
	resource_executor "github.com/koordinator-sh/koordinator/pkg/runtime-manager/resource-executor"
	cri_resource_executor "github.com/koordinator-sh/koordinator/pkg/runtime-manager/resource-executor/cri"
	"github.com/koordinator-sh/koordinator/pkg/runtime-manager/server/utils"
	"github.com/koordinator-sh/koordinator/pkg/runtime-manager/store"
)

const (
	defaultTimeout = 5 * time.Second
)

type RuntimeManagerCriServer struct {
	hookDispatcher *dispatcher.RuntimeHookDispatcher
	backendClient  runtimeapi.RuntimeServiceClient
	store          *store.MetaManager
}

func NewRuntimeManagerCriServer(dispatcher *dispatcher.RuntimeHookDispatcher) *RuntimeManagerCriServer {
	criInterceptor := &RuntimeManagerCriServer{
		hookDispatcher: dispatcher,
		store:          store.NewMetaManager(),
	}
	return criInterceptor
}

func (ci *RuntimeManagerCriServer) Name() string {
	return "RuntimeManagerCriServer"
}

func (ci *RuntimeManagerCriServer) Run() error {
	if err := ci.initBackendServer(utils.DefaultContainerdSocketPath); err != nil {
		return err
	}
	ci.failOver()
	klog.Infof("do failOver done")

	lis, err := net.Listen("unix", utils.DefaultRuntimeManagerSocketPath)
	if err != nil {
		klog.Errorf("fail to create the lis %v", err)
		return err
	}
	grpcServer := grpc.NewServer()
	runtimeapi.RegisterRuntimeServiceServer(grpcServer, ci)
	err = grpcServer.Serve(lis)
	return err
}

func (ci *RuntimeManagerCriServer) getRuntimeHookInfo(serviceType RuntimeServiceType) (config.RuntimeRequestPath,
	resource_executor.RuntimeResourceType) {
	switch serviceType {
	case RunPodSandbox:
		return config.RunPodSandbox, resource_executor.RuntimePodResource
	case CreateContainer:
		// No Nook point in create container, but we need store the container info during container create
		return config.NoneRuntimeHookPath, resource_executor.RuntimeContainerResource
	case StartContainer:
		return config.StartContainer, resource_executor.RuntimeContainerResource
	case StopContainer:
		return config.StopContainer, resource_executor.RuntimeContainerResource
	case UpdateContainerResources:
		return config.UpdateContainerResources, resource_executor.RuntimeContainerResource
	}
	return config.NoneRuntimeHookPath, resource_executor.RuntimeNoopResource
}

func (ci *RuntimeManagerCriServer) interceptRuntimeRequest(serviceType RuntimeServiceType,
	ctx context.Context, request interface{}, handler grpc.UnaryHandler) (interface{}, error) {

	runtimeHookPath, runtimeResourceType := ci.getRuntimeHookInfo(serviceType)
	resourceExecutor := resource_executor.NewRuntimeResourceExecutor(runtimeResourceType, ci.store)

	if err := resourceExecutor.ParseRequest(request); err != nil {
		klog.Errorf("fail to parse request %v %v", request, err)
	}
	defer resourceExecutor.DeleteCheckpointIfNeed(request)

	// pre call hook server
	// TODO deal with the Dispatch response
	if response, err := ci.hookDispatcher.Dispatch(ctx, runtimeHookPath, config.PreHook, resourceExecutor.GenerateHookRequest()); err != nil {
		klog.Errorf("fail to call hook server %v", err)
	} else {
		resourceExecutor.UpdateResource(response)
	}

	// call the backend runtime engine
	res, err := handler(ctx, request)
	if err == nil {
		klog.Infof("%v call on backend %v success", resourceExecutor.GetMetaInfo(), string(runtimeHookPath))
		// store checkpoint info basing request
		// checkpoint only when response success
		if err := resourceExecutor.ResourceCheckPoint(res); err != nil {
			klog.Errorf("fail to checkpoint %v %v", resourceExecutor.GetMetaInfo(), err)
		}
	} else {
		klog.Errorf("%v call on backend %v fail %v", resourceExecutor.GetMetaInfo(), string(runtimeHookPath), err)
	}

	// post call hook server
	// TODO the response
	ci.hookDispatcher.Dispatch(ctx, runtimeHookPath, config.PostHook, resourceExecutor.GenerateHookRequest())
	return res, err
}

func dialer(ctx context.Context, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", addr)
}

func (ci *RuntimeManagerCriServer) initBackendServer(sockPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, sockPath, grpc.WithInsecure(), grpc.WithContextDialer(dialer))
	if err != nil {
		klog.Infof("err to create  %v\n", err)
		return err
	}
	ci.backendClient = runtimeapi.NewRuntimeServiceClient(conn)
	return nil
}

func (ci *RuntimeManagerCriServer) failOver() error {
	podResponse, podErr := ci.backendClient.ListPodSandbox(context.TODO(), &runtimeapi.ListPodSandboxRequest{})
	if podErr != nil {
		return podErr
	}
	containerResponse, containerErr := ci.backendClient.ListContainers(context.TODO(), &runtimeapi.ListContainersRequest{})
	if containerErr != nil {
		return podErr
	}
	for _, pod := range podResponse.Items {
		podResourceExecutor := cri_resource_executor.NewPodResourceExecutor(ci.store)
		podResourceExecutor.ParsePod(pod)
		podResourceExecutor.ResourceCheckPoint(&runtimeapi.RunPodSandboxResponse{
			PodSandboxId: pod.GetId(),
		})
	}

	for _, container := range containerResponse.Containers {
		containerExecutor := cri_resource_executor.NewContainerResourceExecutor(ci.store)
		containerExecutor.ParseContainer(container)
		containerExecutor.ResourceCheckPoint(&runtimeapi.CreateContainerResponse{
			ContainerId: container.GetId(),
		})
	}
	return nil
}
