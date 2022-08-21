package coescheduling

import (
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"encoding/json"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func GetNamespaceSplicingName(namespace, name string) string {
	return namespace + "/" + name
}

func GetGangNameByPod(pod *v1.Pod) string {
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

func ParsePgTimeoutSeconds(timeoutSeconds int32) (time.Duration, error) {
	if timeoutSeconds <= 0 {
		return 0, fmt.Errorf("podGroup timeout value is illegal,timeout Value:%v", timeoutSeconds)
	}
	return time.Duration(timeoutSeconds) * time.Second, nil
}

// StringToGangGroupSlice
// Parse gang group's annotation like :"["nsA/gangA","nsB/gangB"]"  => goLang slice : []string{"nsA/gangA"."nsB/gangB"}
func StringToGangGroupSlice(s string) ([]string, error) {
	gangGroup := make([]string, 0)
	err := json.Unmarshal([]byte(s), &gangGroup)
	if err != nil {
		return gangGroup, err
	}
	return gangGroup, nil
}

func MakePG(name, namespace string, min int32, creationTime *time.Time, minResource *v1.ResourceList) *v1alpha1.PodGroup {
	var ti int32 = 10
	pg := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       v1alpha1.PodGroupSpec{MinMember: min, ScheduleTimeoutSeconds: &ti},
	}
	if creationTime != nil {
		pg.CreationTimestamp = metav1.Time{Time: *creationTime}
	}
	if minResource != nil {
		pg.Spec.MinResources = minResource
	}
	return pg
}
