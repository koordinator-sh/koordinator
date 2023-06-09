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

package util

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func RequestedHostPorts(pod *corev1.Pod) framework.HostPortInfo {
	requestedPorts := framework.HostPortInfo{}
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		for _, podPort := range container.Ports {
			requestedPorts.Add(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
		}
	}
	for i := range pod.Spec.InitContainers {
		container := &pod.Spec.InitContainers[i]
		for _, podPort := range container.Ports {
			requestedPorts.Add(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
		}
	}
	if len(requestedPorts) == 0 {
		return nil
	}
	return requestedPorts
}

func ResetHostPorts(pod *corev1.Pod, ports framework.HostPortInfo) {
	var targetContainer *corev1.Container
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		if len(container.Ports) > 0 {
			container.Ports = nil
			targetContainer = container
		}
	}

	if len(ports) > 0 && targetContainer != nil {
		for ip, protocolPortMap := range ports {
			for ports := range protocolPortMap {
				targetContainer.Ports = append(targetContainer.Ports, corev1.ContainerPort{
					HostPort: ports.Port,
					Protocol: corev1.Protocol(ports.Protocol),
					HostIP:   ip,
				})
			}
		}
	}
}

func CloneHostPorts(ports framework.HostPortInfo) framework.HostPortInfo {
	if len(ports) == 0 {
		return nil
	}
	r := framework.HostPortInfo{}
	for ip, protocolPorts := range ports {
		for v := range protocolPorts {
			r.Add(ip, v.Protocol, v.Port)
		}
	}
	return r
}

func AppendHostPorts(ports framework.HostPortInfo, r framework.HostPortInfo) framework.HostPortInfo {
	if len(r) == 0 {
		return ports
	}
	if ports == nil {
		ports = framework.HostPortInfo{}
	}
	for ip, protocolPorts := range r {
		for v := range protocolPorts {
			ports.Add(ip, v.Protocol, v.Port)
		}
	}
	return ports
}

func RemoveHostPorts(ports framework.HostPortInfo, r framework.HostPortInfo) {
	if len(r) == 0 || len(ports) == 0 {
		return
	}
	for ip, protocolPorts := range r {
		for v := range protocolPorts {
			ports.Remove(ip, v.Protocol, v.Port)
		}
	}
}
