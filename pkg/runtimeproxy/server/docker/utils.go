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
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strings"

	"github.com/docker/docker/api/types/container"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
)

type mockRespWriter struct {
	http.ResponseWriter
	w    io.Writer
	code int
}

func (m *mockRespWriter) Write(p []byte) (n int, err error) {
	m.w.Write(p)
	return m.ResponseWriter.Write(p)
}

func (m *mockRespWriter) WriteHeader(c int) {
	m.code = c
	m.ResponseWriter.WriteHeader(c)
}

func (m mockRespWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := m.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	return h.Hijack()
}

func calculateContentLength(body io.Reader) (l int64, err error) {
	if body == nil {
		return -1, fmt.Errorf("reader is nil")
	}
	buf := &bytes.Buffer{}
	nRead, err := io.Copy(buf, body)
	if err != nil {
		return -1, err
	}
	l = nRead
	return
}

func encodeBody(obj interface{}) (io.Reader, error) {
	if obj == nil {
		return nil, nil
	}

	body, err := encodeData(obj)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func encodeData(data interface{}) (*bytes.Buffer, error) {
	params := bytes.NewBuffer(nil)
	if data != nil {
		if err := json.NewEncoder(params).Encode(data); err != nil {
			return nil, err
		}
	}
	return params, nil
}

func generateNewBody(obj interface{}) (io.ReadCloser, int64, error) {
	nBody, err := encodeBody(obj)
	if err != nil {
		return nil, -1, err
	}
	body := ioutil.NopCloser(nBody)
	nBody, _ = encodeBody(obj)
	newLength, _ := calculateContentLength(nBody)
	return body, newLength, nil
}

func HostConfigToResource(config *container.HostConfig) *v1alpha1.LinuxContainerResources {
	if config == nil {
		return nil
	}
	return &v1alpha1.LinuxContainerResources{
		CpuPeriod:              config.CPUPeriod,
		CpuQuota:               config.CPUQuota,
		CpuShares:              config.CPUShares,
		MemoryLimitInBytes:     config.Memory,
		OomScoreAdj:            int64(config.OomScoreAdj),
		CpusetCpus:             config.CpusetCpus,
		CpusetMems:             config.CpusetMems,
		MemorySwapLimitInBytes: config.MemorySwap,
	}
}

func UpdateHostConfigByResource(config *container.HostConfig, resources *v1alpha1.LinuxContainerResources) *container.HostConfig {
	if config == nil || resources == nil {
		return config
	}
	config.CPUPeriod = resources.CpuPeriod
	config.CPUQuota = resources.CpuQuota
	config.CPUShares = resources.CpuShares
	config.Memory = resources.MemoryLimitInBytes
	config.OomScoreAdj = int(resources.OomScoreAdj)
	config.CpusetCpus = resources.CpusetCpus
	config.CpusetMems = resources.CpusetMems
	config.MemorySwap = resources.MemorySwapLimitInBytes
	return config
}

func UpdateUpdateConfigByResource(containerConfig *container.UpdateConfig, resources *v1alpha1.LinuxContainerResources) {
	if containerConfig == nil || resources == nil {
		return
	}
	containerConfig.CPUPeriod = resources.CpuPeriod
	containerConfig.CPUQuota = resources.CpuQuota
	containerConfig.CPUShares = resources.CpuShares
	containerConfig.Memory = resources.MemoryLimitInBytes
	containerConfig.CpusetCpus = resources.CpusetCpus
	containerConfig.CpusetMems = resources.CpusetMems
	containerConfig.MemorySwap = resources.MemorySwapLimitInBytes

}

func getContainerID(urlPath string) (string, error) {
	tokens := strings.Split(urlPath, "/")
	if len(tokens) < 2 {
		return "", fmt.Errorf("failed to get container id from path %s", urlPath)
	}
	return tokens[len(tokens)-2], nil
}

func splitLabelsAndAnnotations(configs map[string]string) (labels map[string]string, annos map[string]string) {
	labels = make(map[string]string)
	annos = make(map[string]string)
	for k, v := range configs {
		if strings.HasPrefix(k, "annotation.") {
			annos[strings.TrimPrefix(k, "annotation.")] = v
		} else {
			labels[k] = v
		}
	}
	return
}

func ToCriCgroupPath(cgroupDriver, cgroupParent string) string {
	if cgroupDriver != "systemd" {
		return cgroupParent
	}
	if strings.Contains(cgroupParent, "burstable") {
		return fmt.Sprintf("/kubepods.slice/kubepods-burstable.slice/%s", cgroupParent)
	} else if strings.Contains(cgroupParent, "besteffort") {
		return fmt.Sprintf("/kubepods.slice/kubepods-besteffort.slice/%s", cgroupParent)
	} else {
		return fmt.Sprintf("/kubepods.slice/%s", cgroupParent)
	}
}
