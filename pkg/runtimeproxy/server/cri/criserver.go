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
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	runtimeapialpha "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/cmd/koord-runtime-proxy/options"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/dispatcher"
	resource_executor "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/resexecutor"
	cri_resource_executor "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/resexecutor/cri"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/utils"
)

const (
	defaultTimeout = 5 * time.Second
)

type RuntimeRequestInterceptor interface {
	InterceptRuntimeRequest(serviceType RuntimeServiceType, ctx context.Context, request interface{}, handler grpc.UnaryHandler, alphaRuntime bool) (interface{}, error)
}

type criServer struct {
	RuntimeRequestInterceptor
	backendRuntimeServiceClient runtimeapi.RuntimeServiceClient
	backendImageServiceClient   runtimeapi.ImageServiceClient
}

type criAlphaServer struct {
	RuntimeRequestInterceptor
	backendRuntimeServiceClient runtimeapialpha.RuntimeServiceClient
	backendImageServiceClient   runtimeapialpha.ImageServiceClient
}

type RuntimeManagerCriServer struct {
	hookDispatcher *dispatcher.RuntimeHookDispatcher
	criServer      *criServer
	criAlphaServer *criAlphaServer
}

func NewRuntimeManagerCriServer() *RuntimeManagerCriServer {
	criInterceptor := &RuntimeManagerCriServer{
		hookDispatcher: dispatcher.NewRuntimeDispatcher(),
	}
	return criInterceptor
}

func (c *RuntimeManagerCriServer) Name() string {
	return "RuntimeManagerCriServer"
}

func (c *RuntimeManagerCriServer) Run() error {
	if err := c.initCriServer(options.RemoteRuntimeServiceEndpoint, options.RemoteImageServiceEndpoint); err != nil {
		return err
	}
	c.failOver()

	klog.Infof("do failOver done")

	listener, err := net.Listen("unix", options.RuntimeProxyEndpoint)
	if err != nil {
		klog.Errorf("failed to create listener, error: %v", err)
		return err
	}
	grpcServer := grpc.NewServer()
	if c.criServer != nil {
		runtimeapi.RegisterRuntimeServiceServer(grpcServer, c.criServer)
		runtimeapi.RegisterImageServiceServer(grpcServer, c.criServer)
	}
	if c.criAlphaServer != nil {
		runtimeapialpha.RegisterRuntimeServiceServer(grpcServer, c.criAlphaServer)
		runtimeapialpha.RegisterImageServiceServer(grpcServer, c.criAlphaServer)
	}
	err = grpcServer.Serve(listener)
	return err
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

func (c *RuntimeManagerCriServer) InterceptRuntimeRequest(serviceType RuntimeServiceType,
	ctx context.Context, request interface{}, handler grpc.UnaryHandler, alphaRuntime bool) (interface{}, error) {
	runtimeHookPath, runtimeResourceType := c.getRuntimeHookInfo(serviceType)

	resourceExecutor := resource_executor.NewRuntimeResourceExecutor(runtimeResourceType)

	var err error
	if alphaRuntime {
		request, err = alphaObjectToV1Object(request)
		if err != nil {
			return nil, err
		}
	}
	callHookOperation, err := resourceExecutor.ParseRequest(request)
	if err != nil {
		klog.Errorf("fail to parse request %v %v", request, err)
	}
	defer resourceExecutor.DeleteCheckpointIfNeed(request)

	switch callHookOperation {
	case utils.ShouldCallHookPlugin:
		// TODO deal with the Dispatch response
		response, err, policy := c.hookDispatcher.Dispatch(ctx, runtimeHookPath, config.PreHook, resourceExecutor.GenerateHookRequest())
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
	if alphaRuntime {
		request, err = v1ObjectToAlphaObject(request)
		if err != nil {
			return nil, err
		}
	}
	res, err := handler(ctx, request)
	responseConverted := false
	if err == nil {
		if alphaRuntime {
			responseConverted = true
			res, err = alphaObjectToV1Object(res)
			if err != nil {
				return nil, err
			}
		}
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
		c.hookDispatcher.Dispatch(ctx, runtimeHookPath, config.PostHook, resourceExecutor.GenerateHookRequest())
	}
	if responseConverted {
		res, err = v1ObjectToAlphaObject(res)
		if err != nil {
			return nil, err
		}
	}
	return res, err
}

func dialer(ctx context.Context, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", addr)
}

func (c *RuntimeManagerCriServer) initCriServer(runtimeSockPath, imageSockPath string) error {
	generateGrpcConn := func(sockPath string) (*grpc.ClientConn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		return grpc.DialContext(ctx, sockPath, grpc.WithInsecure(), grpc.WithContextDialer(dialer))
	}
	runtimeConn, err := generateGrpcConn(runtimeSockPath)
	if err != nil {
		klog.Errorf("fail to create runtime service client %v", err)
		return err
	} else {
		klog.Infof("success to create runtime client %v", runtimeSockPath)
	}
	imageConn, err := generateGrpcConn(imageSockPath)
	if err != nil {
		klog.Errorf("fail to create image service client %v", err)
		return err
	} else {
		klog.Infof("success to create image client %v", imageSockPath)
	}
	// According to the version of cri api supported by backend runtime, create the corresponding cri server.
	_, v1Err := runtimeapi.NewRuntimeServiceClient(runtimeConn).Version(context.Background(), &runtimeapi.VersionRequest{})
	if v1Err == nil {
		c.criServer = &criServer{
			RuntimeRequestInterceptor:   c,
			backendRuntimeServiceClient: runtimeapi.NewRuntimeServiceClient(runtimeConn),
			backendImageServiceClient:   runtimeapi.NewImageServiceClient(imageConn),
		}
	}
	_, alphaErr := runtimeapialpha.NewRuntimeServiceClient(runtimeConn).Version(context.Background(), &runtimeapialpha.VersionRequest{})
	if alphaErr == nil {
		c.criAlphaServer = &criAlphaServer{
			RuntimeRequestInterceptor:   c,
			backendRuntimeServiceClient: runtimeapialpha.NewRuntimeServiceClient(runtimeConn),
			backendImageServiceClient:   runtimeapialpha.NewImageServiceClient(imageConn),
		}
	}
	if c.criServer == nil && c.criAlphaServer == nil {
		err = fmt.Errorf("%s, %s", v1Err.Error(), alphaErr.Error())
		klog.Errorf("fail to create cri service %v", err)
		return err
	}
	return nil
}

func (c *RuntimeManagerCriServer) failOver() error {
	// Try CRI v1 API first. If the backend runtime does not support the v1 API, fall back to using the v1alpha2 API instead.
	podResponse := &runtimeapi.ListPodSandboxResponse{}
	var err error
	if c.criServer != nil {
		podResponse, err = c.criServer.backendRuntimeServiceClient.ListPodSandbox(context.TODO(), &runtimeapi.ListPodSandboxRequest{})
		if err != nil {
			return err
		}
	} else {
		podResponseAlpha, err := c.criAlphaServer.backendRuntimeServiceClient.ListPodSandbox(context.TODO(), &runtimeapialpha.ListPodSandboxRequest{})
		if err != nil {
			return err
		}
		err = convert(podResponseAlpha, podResponse)
		if err != nil {
			return err
		}
	}

	for _, pod := range podResponse.Items {
		podResourceExecutor := cri_resource_executor.NewPodResourceExecutor()
		podResourceExecutor.ParsePod(pod)
		podResourceExecutor.ResourceCheckPoint(&runtimeapi.RunPodSandboxResponse{
			PodSandboxId: pod.GetId(),
		})
	}

	var containerResponse *runtimeapi.ListContainersResponse
	if c.criServer != nil {
		containerResponse, err = c.criServer.ListContainers(context.TODO(), &runtimeapi.ListContainersRequest{})
		if err != nil {
			return err
		}
	} else {
		containerResponseAlpha, err := c.criAlphaServer.ListContainers(context.TODO(), &runtimeapialpha.ListContainersRequest{})
		if err != nil {
			return err
		}
		err = convert(containerResponseAlpha, podResponse)
		if err != nil {
			return err
		}
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
