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

package oomscoreadj

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestPlugin_refreshForAllPods(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-pod",
			UID:       "xxxxxx",
			Annotations: map[string]string{
				extension.AnnotationOOMScoreAdj: "-500",
			},
		},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{Name: "init-c"},
			},
			Containers: []corev1.Container{
				{Name: "c1"},
			},
		},
		Status: corev1.PodStatus{
			// the pod is Pending while its init container is running
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				{Name: "init-c", ContainerID: "containerd://init111"},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "c1", ContainerID: "containerd://main222"},
			},
		},
	}
	podMeta := &statesinformer.PodMeta{
		Pod:       pod,
		CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
	}
	// write the procs of both containers under the same cgroup parents the reconciler resolves
	initCgroupParent, err := koordletutil.GetContainerCgroupParentDirByID(podMeta.CgroupDir, "containerd://init111")
	assert.NoError(t, err)
	helper.WriteCgroupFileContents(initCgroupParent, sysutil.CPUProcs, "21\n")
	helper.WriteCgroupFileContents(initCgroupParent, sysutil.CPUProcsV2, "21\n")
	mainCgroupParent, err := koordletutil.GetContainerCgroupParentDirByID(podMeta.CgroupDir, "containerd://main222")
	assert.NoError(t, err)
	helper.WriteCgroupFileContents(mainCgroupParent, sysutil.CPUProcs, "22\n")
	helper.WriteCgroupFileContents(mainCgroupParent, sysutil.CPUProcsV2, "22\n")

	fake := sysutil.NewFakeOOMScoreAdj(map[uint32]int64{21: 0, 22: 0}, nil)
	p := newTestPlugin(fake)

	assert.NoError(t, p.refreshForAllPods([]*statesinformer.PodMeta{podMeta}))

	// both the init container and the regular container are reconciled
	gotVal, err := fake.Get(21)
	assert.NoError(t, err)
	assert.Equal(t, int64(-500), gotVal, "init container pid")
	gotVal, err = fake.Get(22)
	assert.NoError(t, err)
	assert.Equal(t, int64(-500), gotVal, "regular container pid")
}
