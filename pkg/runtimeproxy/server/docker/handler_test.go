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
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/server/types"
	"github.com/koordinator-sh/koordinator/pkg/util/httputil"
)

func Test_CreateContainer(t *testing.T) {
	// create docker reverse proxy
	fakeDockerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "create") {
			w.WriteHeader(200)
			w.Write([]byte("{\"Id\": \"containerdID\"}"))
		}
	}))
	defer fakeDockerBackend.Close()
	backendURL, err := url.Parse(fakeDockerBackend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyHandler := httputil.NewSingleHostReverseProxy(backendURL)
	proxyHandler.ErrorLog = log.New(io.Discard, "", 0) // quiet for tests
	manager := NewRuntimeManagerDockerServer()
	manager.reverseProxy = proxyHandler

	frontend := httptest.NewServer(manager)
	defer frontend.Close()
	frontendClient := frontend.Client()
	req, _ := http.NewRequest("POST", frontend.URL, nil)
	cfg := types.ConfigWrapper{
		Config: &container.Config{
			Image: "ubuntu",
			Labels: map[string]string{
				types.ContainerTypeLabelKey: types.ContainerTypeLabelSandbox,
			},
		},
		HostConfig: &container.HostConfig{
			Resources: container.Resources{
				Memory:    100,
				CPUPeriod: 200,
			},
		},
		NetworkingConfig: &network.NetworkingConfig{},
	}
	nBody, err := encodeBody(cfg)
	if err != nil {
		t.Fatal("failed to encode", err)
	}
	req.Body = io.NopCloser(nBody)
	nBody, _ = encodeBody(cfg)
	newLength, _ := calculateContentLength(nBody)
	req.ContentLength = newLength
	req.URL.Path = "/v1.3/containers/create"
	query := url.Values{}
	query.Set("name", "xx_xx_xx_xx_xx_xx")
	req.URL.RawQuery = query.Encode()
	resp, err := frontendClient.Do(req)
	assert.Equal(t, err, nil)
	assert.Equal(t, resp.StatusCode, 200)
}

func Test_StopContainer(t *testing.T) {
	fakeDockerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "stop") {
			w.WriteHeader(200)
		} else if strings.Contains(r.URL.Path, "create") {
			w.WriteHeader(200)
			w.Write([]byte("{\"Id\": \"containerdID\"}"))
		}
	}))
	defer fakeDockerBackend.Close()
	backendURL, err := url.Parse(fakeDockerBackend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyHandler := httputil.NewSingleHostReverseProxy(backendURL)
	proxyHandler.ErrorLog = log.New(io.Discard, "", 0) // quiet for tests
	manager := NewRuntimeManagerDockerServer()
	manager.reverseProxy = proxyHandler

	frontend := httptest.NewServer(manager)
	defer frontend.Close()
	frontendClient := frontend.Client()

	// create a sandbox first
	req, _ := http.NewRequest("POST", frontend.URL, nil)
	cfg := types.ConfigWrapper{
		Config: &container.Config{
			Image: "ubuntu",
			Labels: map[string]string{
				types.ContainerTypeLabelKey: types.ContainerTypeLabelSandbox,
			},
		},
		HostConfig: &container.HostConfig{
			Resources: container.Resources{
				Memory:    100,
				CPUPeriod: 200,
			},
		},
		NetworkingConfig: &network.NetworkingConfig{},
	}
	nBody, err := encodeBody(cfg)
	if err != nil {
		t.Fatal("failed to encode", err)
	}
	req.Body = io.NopCloser(nBody)
	nBody, _ = encodeBody(cfg)
	newLength, _ := calculateContentLength(nBody)
	req.ContentLength = newLength
	req.URL.Path = "/v1.3/containers/create"
	query := url.Values{}
	query.Set("name", "xx_xx_xx_xx_xx_xx")
	req.URL.RawQuery = query.Encode()
	resp, err := frontendClient.Do(req)

	assert.Equal(t, err, nil)
	assert.Equal(t, resp.StatusCode, 200)

	req, _ = http.NewRequest("POST", frontend.URL, nil)
	req.URL.Path = "/v1.3/containers/containerdID/stop"
	resp, err = frontendClient.Do(req)
	assert.Equal(t, err, nil)
	assert.Equal(t, resp.StatusCode, 200)
}

func Test_StartContainer(t *testing.T) {
	fakeDockerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "start") {
			w.WriteHeader(200)
		} else if strings.Contains(r.URL.Path, "create") {
			w.WriteHeader(200)
			w.Write([]byte("{\"Id\": \"containerdID\"}"))
		}
	}))
	defer fakeDockerBackend.Close()
	backendURL, err := url.Parse(fakeDockerBackend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyHandler := httputil.NewSingleHostReverseProxy(backendURL)
	proxyHandler.ErrorLog = log.New(io.Discard, "", 0) // quiet for tests
	manager := NewRuntimeManagerDockerServer()
	manager.reverseProxy = proxyHandler

	frontend := httptest.NewServer(manager)
	defer frontend.Close()
	frontendClient := frontend.Client()

	// create a sandbox first
	req, _ := http.NewRequest("POST", frontend.URL, nil)
	cfg := types.ConfigWrapper{
		Config: &container.Config{
			Image: "ubuntu",
			Labels: map[string]string{
				types.ContainerTypeLabelKey: types.ContainerTypeLabelSandbox,
			},
		},
		HostConfig: &container.HostConfig{
			Resources: container.Resources{
				Memory:    100,
				CPUPeriod: 200,
			},
		},
		NetworkingConfig: &network.NetworkingConfig{},
	}
	nBody, err := encodeBody(cfg)
	if err != nil {
		t.Fatal("failed to encode", err)
	}
	req.Body = io.NopCloser(nBody)
	nBody, _ = encodeBody(cfg)
	newLength, _ := calculateContentLength(nBody)
	req.ContentLength = newLength
	req.URL.Path = "/v1.3/containers/create"
	query := url.Values{}
	query.Set("name", "xx_xx_xx_xx_xx_xx")
	req.URL.RawQuery = query.Encode()
	resp, err := frontendClient.Do(req)

	assert.Equal(t, err, nil)
	assert.Equal(t, resp.StatusCode, 200)

	req, _ = http.NewRequest("POST", frontend.URL, nil)
	req.URL.Path = "/v1.3/containers/containerdID/start"
	resp, err = frontendClient.Do(req)
	assert.Equal(t, err, nil)
	assert.Equal(t, resp.StatusCode, 200)
}

func Test_StopContainerError(t *testing.T) {
	fakeDockerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "stop") {
			w.WriteHeader(200)
		}
	}))
	defer fakeDockerBackend.Close()
	backendURL, err := url.Parse(fakeDockerBackend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyHandler := httputil.NewSingleHostReverseProxy(backendURL)
	proxyHandler.ErrorLog = log.New(io.Discard, "", 0) // quiet for tests
	manager := NewRuntimeManagerDockerServer()
	manager.reverseProxy = proxyHandler

	frontend := httptest.NewServer(manager)
	defer frontend.Close()
	frontendClient := frontend.Client()

	req, _ := http.NewRequest("POST", frontend.URL, nil)
	req.URL.Path = "/v1.3/containers/xxxxxx/stop"
	resp, err := frontendClient.Do(req)
	assert.Equal(t, err, nil)
	assert.Equal(t, resp.StatusCode, 500)
}

func Test_UpdateContainer(t *testing.T) {
	fakeDockerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "update") {
			w.WriteHeader(200)
		} else if strings.Contains(r.URL.Path, "create") {
			w.WriteHeader(200)
			w.Write([]byte("{\"Id\": \"updateContainerdID\"}"))
		}
	}))
	defer fakeDockerBackend.Close()
	backendURL, err := url.Parse(fakeDockerBackend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyHandler := httputil.NewSingleHostReverseProxy(backendURL)
	proxyHandler.ErrorLog = log.New(io.Discard, "", 0) // quiet for tests
	manager := NewRuntimeManagerDockerServer()
	manager.reverseProxy = proxyHandler

	frontend := httptest.NewServer(manager)
	defer frontend.Close()
	frontendClient := frontend.Client()

	// create a sandbox first
	req, _ := http.NewRequest("POST", frontend.URL, nil)
	cfg := types.ConfigWrapper{
		Config: &container.Config{
			Image: "ubuntu",
			Labels: map[string]string{
				types.ContainerTypeLabelKey: types.ContainerTypeLabelSandbox,
			},
		},
		HostConfig: &container.HostConfig{
			Resources: container.Resources{
				Memory:    100,
				CPUPeriod: 200,
			},
		},
		NetworkingConfig: &network.NetworkingConfig{},
	}
	nBody, err := encodeBody(cfg)
	if err != nil {
		t.Fatal("failed to encode", err)
	}
	req.Body = io.NopCloser(nBody)
	nBody, _ = encodeBody(cfg)
	newLength, _ := calculateContentLength(nBody)
	req.ContentLength = newLength
	req.URL.Path = "/v1.3/containers/create"
	query := url.Values{}
	query.Set("name", "xx_xx_xx_xx_xx_xx")
	req.URL.RawQuery = query.Encode()
	resp, err := frontendClient.Do(req)

	assert.Equal(t, err, nil)
	assert.Equal(t, resp.StatusCode, 200)

	// create a update container req
	req, _ = http.NewRequest("POST", frontend.URL, nil)
	updateCfg := &container.UpdateConfig{
		Resources: container.Resources{
			Memory:    200,
			CPUPeriod: 100,
		},
	}
	nBody, err = encodeBody(updateCfg)
	if err != nil {
		t.Fatal("failed to encode", err)
	}
	req.Body = io.NopCloser(nBody)
	nBody, _ = encodeBody(updateCfg)
	newLength, _ = calculateContentLength(nBody)
	req.ContentLength = newLength
	req.URL.Path = "/v1.3/containers/updateContainerdID/update"
	resp, err = frontendClient.Do(req)

	assert.Equal(t, err, nil)
	assert.Equal(t, resp.StatusCode, 200)
}
