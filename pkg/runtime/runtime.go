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

package runtime

import (
	"fmt"
	"os"
	"path"
	"sync"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/runtime/handler"
)

var (
	DockerEndpoint      = path.Join("/var/run/", "docker.sock")
	ContainerdEndpoint1 = path.Join("/var/run/", "containerd.sock")
	ContainerdEndpoint2 = path.Join("/var/run/", "containerd/containerd.sock")
)

var (
	DockerHandler     handler.ContainerRuntimeHandler
	ContainerdHandler handler.ContainerRuntimeHandler
	mutex             = &sync.Mutex{}
)

func GetRuntimeHandler(runtimeType string) (handler.ContainerRuntimeHandler, error) {
	mutex.Lock()
	defer mutex.Unlock()

	switch runtimeType {
	case "docker":
		return getDockerHandler()
	case "containerd":
		return getContainerdHandler()
	default:
		return nil, fmt.Errorf("runtime type %v is not supported", runtimeType)
	}
}

func getDockerHandler() (handler.ContainerRuntimeHandler, error) {
	if DockerHandler != nil {
		return DockerHandler, nil
	}

	unixEndpoint, err := getDockerEndpoint()
	if err != nil {
		klog.Errorf("failed to get docker endpoint, error: %v", err)
		return nil, err
	}

	DockerHandler, err = handler.NewDockerRuntimeHandler(unixEndpoint)
	if err != nil {
		klog.Errorf("failed to create docker runtime handler, error: %v", err)
		return nil, err
	}

	return DockerHandler, nil
}

func getDockerEndpoint() (string, error) {
	if isFile(DockerEndpoint) {
		return fmt.Sprintf("unix://%s", DockerEndpoint), nil
	}
	return "", fmt.Errorf("docker endpoint does not exist")
}

func getContainerdHandler() (handler.ContainerRuntimeHandler, error) {
	if ContainerdHandler != nil {
		return ContainerdHandler, nil
	}

	unixEndpoint, err := getContainerdEndpoint()
	if err != nil {
		klog.Errorf("failed to get containerd endpoint, error: %v", err)
		return nil, err
	}

	ContainerdHandler, err = handler.NewContainerdRuntimeHandler(unixEndpoint)
	if err != nil {
		klog.Errorf("failed to create containerd runtime handler, error: %v", err)
		return nil, err
	}

	return ContainerdHandler, nil
}

func getContainerdEndpoint() (string, error) {
	if isFile(ContainerdEndpoint1) {
		return fmt.Sprintf("unix://%s", ContainerdEndpoint1), nil
	}

	if isFile(ContainerdEndpoint2) {
		return fmt.Sprintf("unix://%s", ContainerdEndpoint2), nil
	}

	return "", fmt.Errorf("containerd endpoint does not exist")
}

func isFile(path string) bool {
	s, err := os.Stat(path)
	if err != nil || s == nil {
		return false
	}
	return !s.IsDir()
}
