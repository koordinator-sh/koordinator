package gang

import (
	v1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func GetNamespaceSplicingName(namespace, name string) string {
	return namespace + "/" + name
}

func GetPodGroupNameByPod(pod *v1.Pod) string {
	if pod == nil {
		return ""
	}
	var gangName string
	gangName = pod.Labels[v1alpha1.PodGroupLabel]
	if gangName == "" {
		gangName = pod.Annotations[extension.AnnotationGangName]
	}
	return gangName
}

func PgFromAnnotation(pod *v1.Pod) bool {
	gangName := pod.Annotations[extension.AnnotationGangName]
	return !(gangName == "")
}
