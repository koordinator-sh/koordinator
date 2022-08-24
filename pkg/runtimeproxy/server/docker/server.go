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

package docker

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/cmd/koord-runtime-proxy/options"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/dispatcher"
	resource_executor "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/resexecutor"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/server/types"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/store"
	"github.com/koordinator-sh/koordinator/pkg/util/httputil"
)

type RuntimeManagerDockerServer struct {
	dispatcher   *dispatcher.RuntimeHookDispatcher
	reverseProxy *httputil.ReverseProxy
	router       map[*regexp.Regexp]func(context.Context, http.ResponseWriter, *http.Request)
	cgroupDriver string
}

type proxyDockerClient interface {
	Info(ctx context.Context) (dockertypes.Info, error)
	ContainerList(ctx context.Context, options dockertypes.ContainerListOptions) ([]dockertypes.Container, error)
	ContainerInspect(ctx context.Context, containerID string) (dockertypes.ContainerJSON, error)
}

func (d *RuntimeManagerDockerServer) Name() string {
	return "RuntimeManagerDockerServer"
}

func NewRuntimeManagerDockerServer() *RuntimeManagerDockerServer {
	interceptor := &RuntimeManagerDockerServer{
		dispatcher: dispatcher.NewRuntimeDispatcher(),
	}
	interceptor.router = map[*regexp.Regexp]func(context.Context, http.ResponseWriter, *http.Request){
		regexp.MustCompile(`^/(v\d\.\d+/)?containers(/\w+)?/update$`): interceptor.HandleUpdateContainer,
		regexp.MustCompile(`^/(v\d\.\d+/)?containers/create$`):        interceptor.HandleCreateContainer,
		regexp.MustCompile(`^/(v\d\.\d+/)?containers(/\w+)?/start$`):  interceptor.HandleStartContainer,
		regexp.MustCompile(`^/(v\d\.\d+/)?containers(/\w+)?/stop`):    interceptor.HandleStopContainer,
	}
	return interceptor
}

func (d *RuntimeManagerDockerServer) Direct(wr http.ResponseWriter, req *http.Request) string {
	out := &bytes.Buffer{}
	multi := &mockRespWriter{wr, out, 0}
	d.reverseProxy.ServeHTTP(multi, req)
	resp := out.String()
	klog.V(5).Infof("response: %d %s, headers: %q", multi.code, resp, wr.Header())
	return resp
}

func (d *RuntimeManagerDockerServer) ServeHTTP(wr http.ResponseWriter, req *http.Request) {
	ctx := context.TODO()
	klog.Infof("req path: %s, req method: %s", req.URL.Path, req.Method)
	for reg, handler := range d.router {
		if reg.MatchString(req.URL.Path) {
			handler(ctx, wr, req)
			return
		}
	}
	// fall back to reverse proxy
	d.Direct(wr, req)
}

func (d *RuntimeManagerDockerServer) failOver(dockerClient proxyDockerClient) error {

	type dockerWrapper struct {
		dockertypes.ContainerJSON
		dockertypes.Container
	}
	sandboxes := []dockerWrapper{}
	containers := []dockerWrapper{}
	cs, err := dockerClient.ContainerList(context.TODO(), dockertypes.ContainerListOptions{All: true})
	if err != nil {
		klog.Errorf("Failed to get container list in failover, err: %v", err)
		return err
	}
	for _, c := range cs {
		containerJson, err := dockerClient.ContainerInspect(context.TODO(), c.ID)
		if err != nil {
			klog.Errorf("Failed to get container detail of id %s", c.ID)
			continue
		}
		runtimeResourceType := GetRuntimeResourceType(c.Labels)
		if runtimeResourceType == resource_executor.RuntimeContainerResource {
			containers = append(containers, dockerWrapper{
				ContainerJSON: containerJson,
				Container:     c,
			})
		} else {
			sandboxes = append(sandboxes, dockerWrapper{
				ContainerJSON: containerJson,
				Container:     c,
			})
		}
	}

	// need to backup pod meta first
	for _, s := range sandboxes {
		store.WritePodSandboxInfo(s.ID, &store.PodSandboxInfo{
			PodSandboxHookRequest: &v1alpha1.PodSandboxHookRequest{
				Labels:       s.Labels,
				Annotations:  s.Labels,
				CgroupParent: s.ContainerJSON.HostConfig.CgroupParent,
				PodMeta: &v1alpha1.PodSandboxMetadata{
					Name: s.Name,
					Uid:  s.ID,
				},
				RuntimeHandler: "Docker",
				Resources:      HostConfigToResource(s.ContainerJSON.HostConfig),
			},
		})
	}

	for _, c := range containers {
		cInfo := &store.ContainerInfo{
			ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
				ContainerResources:   HostConfigToResource(c.ContainerJSON.HostConfig),
				ContainerAnnotations: c.Labels,
				ContainerMeta: &v1alpha1.ContainerMetadata{
					Name: c.Name,
					Id:   c.ID,
				},
			},
		}
		podID := c.Labels[types.SandboxIDLabelKey]
		podCheckPoint := store.GetPodSandboxInfo(podID)
		if podCheckPoint != nil {
			cInfo.ContainerResourceHookRequest.PodMeta = podCheckPoint.PodMeta
			cInfo.ContainerResourceHookRequest.PodResources = podCheckPoint.Resources
		}
		if c.Config != nil {
			cInfo.ContainerResourceHookRequest.ContainerEnvs = splitDockerEnv(c.Config.Env)
		}
		store.WriteContainerInfo(c.ID, cInfo)
	}
	info, err := dockerClient.Info(context.TODO())
	if err != nil {
		klog.Errorf("Failed to get docker server info, err: %v", err)
		return err
	}
	d.cgroupDriver = info.CgroupDriver
	return nil
}

func (d *RuntimeManagerDockerServer) Run() error {
	d.reverseProxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			param := ""
			if len(req.URL.RawQuery) > 0 {
				param = "?" + req.URL.RawQuery
			}
			u, _ := url.Parse("http://docker" + req.URL.Path + param)
			*req.URL = *u
		},
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", options.RemoteRuntimeServiceEndpoint)
			}},
	}

	dockerClient, err := client.NewClientWithOpts(client.WithHost("unix://"+options.RemoteRuntimeServiceEndpoint), client.WithVersion("1.39"))
	if err != nil {
		return err
	}

	err = d.failOver(dockerClient)
	if err != nil {
		panic(fmt.Sprintf("Failed to backup container info from backend, err: %v", err))
	}

	lis, err := net.Listen("unix", options.RuntimeProxyEndpoint)
	if err != nil {
		klog.Fatal("Failed to create the lis %v", err)
	}
	if err := http.Serve(lis, d); err != nil {
		klog.Fatal("ListenAndServe:", err)
	}
	return nil
}
