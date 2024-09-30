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
	"sync"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime/handler"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

var (
	DockerHandler     handler.ContainerRuntimeHandler
	ContainerdHandler handler.ContainerRuntimeHandler
	PouchHandler      handler.ContainerRuntimeHandler
	CrioHandler       handler.ContainerRuntimeHandler
	mutex             = &sync.Mutex{}
)

func GetRuntimeHandler(runtimeType string) (handler.ContainerRuntimeHandler, error) {
	mutex.Lock()
	defer mutex.Unlock()

	switch runtimeType {
	case system.RuntimeTypeDocker:
		return getDockerHandler()
	case system.RuntimeTypeContainerd:
		return getContainerdHandler()
	case system.RuntimeTypePouch:
		return getPouchHandler()
	case system.RuntimeTypeCrio:
		return getCrioHandler()
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
	if dockerEndpoint := handler.GetDockerEndpoint(); isFile(dockerEndpoint) {
		return fmt.Sprintf("unix://%s", dockerEndpoint), nil
	}
	if len(system.Conf.DockerEndPoint) > 0 && isFile(system.Conf.DockerEndPoint) {
		klog.Infof("find docker Endpoint : %v", system.Conf.DockerEndPoint)
		return fmt.Sprintf("unix://%s", system.Conf.DockerEndPoint), nil
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
	if containerdEndpoint := handler.GetContainerdEndpoint(); isFile(containerdEndpoint) {
		return fmt.Sprintf("unix://%s", containerdEndpoint), nil
	}

	if containerdEndpoint2 := handler.GetContainerdEndpoint2(); isFile(containerdEndpoint2) {
		return fmt.Sprintf("unix://%s", containerdEndpoint2), nil
	}

	if len(system.Conf.ContainerdEndPoint) > 0 && isFile(system.Conf.ContainerdEndPoint) {
		klog.Infof("find containerd Endpoint : %v", system.Conf.ContainerdEndPoint)
		return fmt.Sprintf("unix://%s", system.Conf.ContainerdEndPoint), nil
	}

	return "", fmt.Errorf("containerd endpoint does not exist")
}

func getPouchHandler() (handler.ContainerRuntimeHandler, error) {
	if PouchHandler != nil {
		return PouchHandler, nil
	}

	unixEndpoint, err := getPouchEndpoint()
	if err != nil {
		klog.Errorf("failed to get pouch endpoint, error: %v", err)
		return nil, err
	}

	PouchHandler, err = handler.NewPouchRuntimeHandler(unixEndpoint)
	if err != nil {
		klog.Errorf("failed to create pouch runtime handler, error: %v", err)
		return nil, err
	}

	return PouchHandler, nil
}

func getPouchEndpoint() (string, error) {
	if pouchEndpoint := handler.GetPouchEndpoint(); isFile(pouchEndpoint) {
		return fmt.Sprintf("unix://%s", pouchEndpoint), nil
	}

	if len(system.Conf.PouchEndpoint) > 0 && isFile(system.Conf.PouchEndpoint) {
		klog.Infof("find pouch Endpoint : %v", system.Conf.PouchEndpoint)
		return fmt.Sprintf("unix://%s", system.Conf.PouchEndpoint), nil
	}

	return "", fmt.Errorf("pouch endpoint does not exist")
}

func getCrioHandler() (handler.ContainerRuntimeHandler, error) {
	if CrioHandler != nil {
		return CrioHandler, nil
	}

	unixEndpoint, err := getCrioEndpoint()
	if err != nil {
		klog.Errorf("failed to get cri-o endpoint, error: %v", err)
		return nil, err
	}

	CrioHandler, err = handler.NewCrioRuntimeHandler(unixEndpoint)
	if err != nil {
		klog.Errorf("failed to create cri-o runtime handler, error: %v", err)
		return nil, err
	}

	return CrioHandler, nil
}

func getCrioEndpoint() (string, error) {
	if crioEndpoint := handler.GetCrioEndpoint(); isFile(crioEndpoint) {
		return fmt.Sprintf("unix://%s", crioEndpoint), nil
	}

	if crioEndpoint2 := handler.GetCrioEndpoint2(); isFile(crioEndpoint2) {
		return fmt.Sprintf("unix://%s", crioEndpoint2), nil
	}

	if len(system.Conf.CrioEndPoint) > 0 && isFile(system.Conf.CrioEndPoint) {
		klog.Infof("find cri-o Endpoint : %v", system.Conf.CrioEndPoint)
		return fmt.Sprintf("unix://%s", system.Conf.CrioEndPoint), nil
	}

	return "", fmt.Errorf("cri-o endpoint does not exist")
}

func isFile(path string) bool {
	s, err := os.Stat(path)
	if err != nil || s == nil {
		return false
	}
	return !s.IsDir()
}
