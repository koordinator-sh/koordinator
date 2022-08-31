package utils

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func GetPodMetas(pods []*corev1.Pod) []*statesinformer.PodMeta {
	podMetas := make([]*statesinformer.PodMeta, len(pods))

	for index, pod := range pods {
		cgroupDir := util.GetPodKubeRelativePath(pod)
		podMeta := &statesinformer.PodMeta{CgroupDir: cgroupDir, Pod: pod.DeepCopy()}
		podMetas[index] = podMeta
	}

	return podMetas
}
