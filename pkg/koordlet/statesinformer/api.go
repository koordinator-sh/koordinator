package statesinformer

import corev1 "k8s.io/api/core/v1"

type PodMeta struct {
	Pod       *corev1.Pod
	CgroupDir string
}

func (in *PodMeta) DeepCopy() *PodMeta {
	out := new(PodMeta)
	out.Pod = in.Pod.DeepCopy()
	out.CgroupDir = in.CgroupDir
	return out
}
