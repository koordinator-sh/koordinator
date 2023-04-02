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
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/dispatcher"
	resource_executor "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/resexecutor"
	cri_resource_executor "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/resexecutor/cri"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/utils"
)

const (
	defaultTimeout = 5 * time.Second
)

type RuntimeManagerCriServer struct {
	dispatcher    *dispatcher.RuntimeHookDispatcher
	runtimeClient runtimeapi.RuntimeServiceClient
	imageClient   runtimeapi.ImageServiceClient
	proxyConfig   *config.Config
}

func NewRuntimeManagerCRIServer(proxyConfig *config.Config) (*RuntimeManagerCriServer, error) {
	if proxyConfig == nil {
		return nil, fmt.Errorf("runtime proxy config is nil")
	}

	runtimeClient, imageClient, err := createRuntimeAndImageClient(proxyConfig.RemoteRuntimeServiceEndpoint, proxyConfig.RemoteImageServiceEndpoint)
	if err != nil {
		return nil, err
	}

	interceptor := &RuntimeManagerCriServer{
		dispatcher:    dispatcher.NewRuntimeDispatcher(),
		runtimeClient: runtimeClient,
		imageClient:   imageClient,
		proxyConfig:   proxyConfig,
	}
	return interceptor, nil
}

func (c *RuntimeManagerCriServer) Name() string {
	return "RuntimeManagerCRIServer"
}

func (c *RuntimeManagerCriServer) Run() error {
	klog.V(2).Infof("Start runtime proxy grpc server")

	if err := c.failOver(); err != nil {
		return fmt.Errorf("Failed to backup container info from runtime service, err: %v", err)
	}

	listener, err := net.Listen("unix", c.proxyConfig.RuntimeProxyEndpoint)
	if err != nil {
		klog.Errorf("Failed to listen on %q: %v", c.proxyConfig.RuntimeProxyEndpoint, err)
		return err
	}

	// Create the grpc server and register runtime and image services.
	grpcServer := grpc.NewServer()
	runtimeapi.RegisterRuntimeServiceServer(grpcServer, c)
	runtimeapi.RegisterImageServiceServer(grpcServer, c)

	go func() {
		err := grpcServer.Serve(listener)
		if err != nil {
			panic(fmt.Sprintf("Failed to serve connections: %v", err))
		}
	}()

	return nil
}

func (c *RuntimeManagerCriServer) getRuntimeHookInfo(serviceType RuntimeServiceType) (config.RuntimeRequestPath,
	resource_executor.RuntimeResourceType) {
	switch serviceType {
	case RunPodSandbox:
		return config.RunPodSandbox, resource_executor.RuntimePodResource
	case StopPodSandbox:
		return config.StopPodSandbox, resource_executor.RuntimePodResource
	case CreateContainer:
		return config.CreateContainer, resource_executor.RuntimeContainerResource
	case StartContainer:
		return config.StartContainer, resource_executor.RuntimeContainerResource
	case StopContainer:
		return config.StopContainer, resource_executor.RuntimeContainerResource
	case UpdateContainerResources:
		return config.UpdateContainerResources, resource_executor.RuntimeContainerResource
	}
	return config.NoneRuntimeHookPath, resource_executor.RuntimeNoopResource
}

func (c *RuntimeManagerCriServer) interceptRuntimeRequest(serviceType RuntimeServiceType,
	ctx context.Context, request interface{}, handler grpc.UnaryHandler) (interface{}, error) {
	runtimeHookPath, runtimeResourceType := c.getRuntimeHookInfo(serviceType)
	resourceExecutor := resource_executor.NewRuntimeResourceExecutor(runtimeResourceType)

	callHookOperation, err := resourceExecutor.ParseRequest(request)
	if err != nil {
		klog.Errorf("fail to parse request %v %v", request, err)
	}
	defer resourceExecutor.DeleteCheckpointIfNeed(request)

	switch callHookOperation {
	case utils.ShouldCallHookPlugin:
		// TODO deal with the Dispatch response
		response, err, policy := c.dispatcher.Dispatch(ctx, runtimeHookPath, config.PreHook, resourceExecutor.GenerateHookRequest())
		if err != nil {
			klog.Errorf("fail to call hook server %v", err)
			if policy == config.PolicyFail {
				return nil, fmt.Errorf("hook server err: %v", err)
			}
		} else if response != nil {
			if err = resourceExecutor.UpdateRequest(response, request); err != nil {
				klog.Errorf("failed to update cri request %v", err)
			}
		}
	}
	// call the backend runtime engine
	res, err := handler(ctx, request)
	if err == nil {
		klog.Infof("%v call containerd %v success", resourceExecutor.GetMetaInfo(), string(runtimeHookPath))
		// store checkpoint info basing request only when response success
		if err := resourceExecutor.ResourceCheckPoint(res); err != nil {
			klog.Errorf("fail to checkpoint %v %v", resourceExecutor.GetMetaInfo(), err)
		}
	} else {
		klog.Errorf("%v call containerd %v fail %v", resourceExecutor.GetMetaInfo(), string(runtimeHookPath), err)
	}
	switch callHookOperation {
	case utils.ShouldCallHookPlugin:
		// post call hook server
		// TODO the response
		c.dispatcher.Dispatch(ctx, runtimeHookPath, config.PostHook, resourceExecutor.GenerateHookRequest())
	}
	return res, err
}

func dialer(ctx context.Context, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", addr)
}

func createRuntimeAndImageClient(runtimeSockPath, imageSockPath string) (runtimeapi.RuntimeServiceClient, runtimeapi.ImageServiceClient, error) {
	if runtimeSockPath == "" {
		return nil, nil, fmt.Errorf("must be set remote runtime sock path")
	}

	if imageSockPath == "" {
		imageSockPath = runtimeSockPath
	}

	generateGrpcConn := func(sockPath string) (*grpc.ClientConn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		return grpc.DialContext(ctx, sockPath, grpc.WithInsecure(), grpc.WithContextDialer(dialer))
	}

	runtimeConn, err := generateGrpcConn(runtimeSockPath)
	if err != nil {
		return nil, nil, err
	}
	runtimeServiceClient := runtimeapi.NewRuntimeServiceClient(runtimeConn)

	imageConn, err := generateGrpcConn(imageSockPath)
	if err != nil {
		klog.Errorf("fail to create image service client %v", err)
		return nil, nil, err
	}
	imageServiceClient := runtimeapi.NewImageServiceClient(imageConn)

	return runtimeServiceClient, imageServiceClient, nil
}

func (c *RuntimeManagerCriServer) failOver() error {
	podResponse, podErr := c.runtimeClient.ListPodSandbox(context.TODO(), &runtimeapi.ListPodSandboxRequest{})
	if podErr != nil {
		return podErr
	}
	for _, pod := range podResponse.Items {
		podResourceExecutor := cri_resource_executor.NewPodResourceExecutor()
		podResourceExecutor.ParsePod(pod)
		podResourceExecutor.ResourceCheckPoint(&runtimeapi.RunPodSandboxResponse{
			PodSandboxId: pod.GetId(),
		})
	}

	containerResponse, containerErr := c.runtimeClient.ListContainers(context.TODO(), &runtimeapi.ListContainersRequest{})
	if containerErr != nil {
		return containerErr
	}
	for _, container := range containerResponse.Containers {
		containerExecutor := cri_resource_executor.NewContainerResourceExecutor()
		if err := containerExecutor.ParseContainer(container); err != nil {
			klog.Errorf("failed to parse container %s, err: %v", container.Id, err)
			continue
		}
		containerExecutor.ResourceCheckPoint(&runtimeapi.CreateContainerResponse{
			ContainerId: container.GetId(),
		})
	}

	return nil
}
