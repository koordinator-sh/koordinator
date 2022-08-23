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
	"net"
	"net/http"
	"path"
	"strings"

	"k8s.io/klog/v2"

	"github.com/docker/docker/api/types/container"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	resource_executor "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/resexecutor"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/server/types"
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
	// for docker + systemd combination, the cgroup parent is pod dir, for example:
	// kubepods-besteffort-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice,
	// so the full path for it is :
	// /kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice
	cgroupHierarcy := strings.Split(cgroupParent, "-")
	prefix := cgroupHierarcy[0]
	fullPath := "/"
	for i := 0; i < len(cgroupHierarcy)-1; i++ {
		fullPath = path.Join(fullPath, fmt.Sprintf("%s.slice", prefix))
		prefix = fmt.Sprintf("%s-%s", prefix, cgroupHierarcy[i+1])
	}
	fullPath = path.Join(fullPath, prefix)
	return fullPath
}

func GetRuntimeResourceType(labels map[string]string) resource_executor.RuntimeResourceType {
	if labels[types.ContainerTypeLabelKey] == types.ContainerTypeLabelSandbox {
		return resource_executor.RuntimePodResource
	}
	return resource_executor.RuntimeContainerResource
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

func UpdateUpdateConfigByResource(containerConfig *container.UpdateConfig, resources *v1alpha1.LinuxContainerResources) *container.UpdateConfig {
	if containerConfig == nil || resources == nil {
		return containerConfig
	}
	containerConfig.CPUPeriod = resources.CpuPeriod
	containerConfig.CPUQuota = resources.CpuQuota
	containerConfig.CPUShares = resources.CpuShares
	containerConfig.Memory = resources.MemoryLimitInBytes
	containerConfig.CpusetCpus = resources.CpusetCpus
	containerConfig.CpusetMems = resources.CpusetMems
	containerConfig.MemorySwap = resources.MemorySwapLimitInBytes
	return containerConfig
}

func MergeResourceByUpdateConfig(resources *v1alpha1.LinuxContainerResources, containerConfig *container.UpdateConfig) *v1alpha1.LinuxContainerResources {
	if containerConfig == nil || resources == nil {
		return resources
	}
	if containerConfig.CPUPeriod > 0 {
		resources.CpuPeriod = containerConfig.CPUPeriod
	}
	if containerConfig.CPUQuota > 0 {
		resources.CpuQuota = containerConfig.CPUQuota
	}
	if containerConfig.CPUShares > 0 {
		resources.CpuShares = containerConfig.CPUShares
	}
	if containerConfig.Memory > 0 {
		resources.MemoryLimitInBytes = containerConfig.Memory
	}
	if containerConfig.CpusetCpus != "" {
		resources.CpusetCpus = containerConfig.CpusetCpus
	}
	if containerConfig.CpusetMems != "" {
		resources.CpusetMems = containerConfig.CpusetMems
	}
	if containerConfig.MemorySwap > 0 {
		resources.MemorySwapLimitInBytes = containerConfig.MemorySwap
	}
	return resources
}

// generateExpectedCgroupParent is adapted from Dockershim
func generateExpectedCgroupParent(cgroupDriver, cgroupParent string) string {
	if cgroupParent != "" {
		// if docker uses the systemd cgroup driver, it expects *.slice style names for cgroup parent.
		// if we configured kubelet to use --cgroup-driver=cgroupfs, and docker is configured to use systemd driver
		// docker will fail to launch the container because the name we provide will not be a valid slice.
		// this is a very good thing.
		if cgroupDriver == "systemd" {
			// Pass only the last component of the cgroup path to systemd.
			cgroupParent = path.Base(cgroupParent)
		}
	}
	klog.V(4).Infof("Setting cgroup parent to: %q", cgroupParent)
	return cgroupParent
}

func splitDockerEnv(dockerEnvs []string) map[string]string {
	res := make(map[string]string)
	for _, str := range dockerEnvs {
		tokens := strings.Split(str, "=")
		if len(tokens) != 2 {
			continue
		}
		res[tokens[0]] = tokens[1]
	}
	return res
}

func generateEnvList(envs map[string]string) (result []string) {
	for key, value := range envs {
		result = append(result, fmt.Sprintf("%s=%s", key, value))
	}
	return
}
