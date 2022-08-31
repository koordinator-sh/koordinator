package utils

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/runtime"
)

// KillContainers kills containers inside the pod
func KillContainers(pod *corev1.Pod, message string) {
	for _, container := range pod.Spec.Containers {
		containerID, containerStatus, err := util.FindContainerIdAndStatusByName(&pod.Status, container.Name)
		if err != nil {
			klog.Errorf("failed to find container id and status, error: %v", err)
			return
		}

		if containerStatus == nil || containerStatus.State.Running == nil {
			return
		}

		if containerID != "" {
			runtimeType, _, _ := util.ParseContainerId(containerStatus.ContainerID)
			runtimeHandler, err := runtime.GetRuntimeHandler(runtimeType)
			if err != nil || runtimeHandler == nil {
				klog.Errorf("%s, kill container(%s) error! GetRuntimeHandler fail! error: %v", message, containerStatus.ContainerID, err)
				continue
			}
			if err := runtimeHandler.StopContainer(containerID, 0); err != nil {
				klog.Errorf("%s, stop container error! error: %v", message, err)
			}
		} else {
			klog.Warningf("%s, get container ID failed, pod %s/%s containerName %s status: %v", message, pod.Namespace, pod.Name, container.Name, pod.Status.ContainerStatuses)
		}
	}
}


