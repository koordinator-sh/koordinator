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

package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/cgroup"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/podcgroup"
)

func makeMemoryPodCgroup(path string, annotations map[string]string) *podcgroup.PodCgroup {
	return &podcgroup.PodCgroup{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pod",
				Namespace:   "default",
				UID:         types.UID("pod"),
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "container",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
							Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")},
						},
					},
				},
			},
		},
		Cgroup: cgroup.NewCgroup(path, [2]int64{0, 0}),
	}
}

func TestMemorySuppressInitializesCounterForAnnotatedPodOnExec(t *testing.T) {
	pc := makeMemoryPodCgroup("/not-exist", map[string]string{AnnotationMemorySuppress: "true"})
	op := &MemorySuppress{
		MinSpot:     0.5,
		MaxSpot:     0.9,
		GrowPeriods: 10,
		KillPeriods: 60,
	}

	assert.NotPanics(t, func() {
		_ = op.Exec(map[types.UID]*podcgroup.PodCgroup{pc.Pod.UID: pc}, &corev1.Node{})
	})
}
