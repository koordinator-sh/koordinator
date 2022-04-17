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

package statesinformer

import (
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_genPodCgroupParentDirWithSystemdDriver(t *testing.T) {
	sysutil.SetupCgroupPathFormatter(sysutil.Systemd)
	defer sysutil.SetupCgroupPathFormatter(sysutil.Systemd)
	tests := []struct {
		name string
		args *corev1.Pod
		want string
	}{
		{
			name: "Guaranteed",
			args: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: "111-222-333",
				},
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSGuaranteed,
				},
			},
			want: "/kubepods-pod111_222_333.slice",
		},
		{
			name: "BestEffort",
			args: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: "111-222-333",
				},
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSBestEffort,
				},
			},
			want: "/kubepods-besteffort.slice/kubepods-besteffort-pod111_222_333.slice",
		},
		{
			name: "Burstable",
			args: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: "111-222-333",
				},
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSBurstable,
				},
			},
			want: "/kubepods-burstable.slice/kubepods-burstable-pod111_222_333.slice",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filepath.Join("/", genPodCgroupParentDir(tt.args))
			if tt.want != got {
				t.Errorf("genPodCgroupParentDir want %v but got %v", tt.want, got)
			}
		})
	}
}

func Test_genPodCgroupParentDirWithCgroupfsDriver(t *testing.T) {
	sysutil.SetupCgroupPathFormatter(sysutil.Cgroupfs)
	defer sysutil.SetupCgroupPathFormatter(sysutil.Systemd)
	tests := []struct {
		name string
		args *corev1.Pod
		want string
	}{
		{
			name: "Guaranteed",
			args: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: "111-222-333",
				},
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSGuaranteed,
				},
			},
			want: "/pod111-222-333",
		},
		{
			name: "BestEffort",
			args: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: "111-222-333",
				},
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSBestEffort,
				},
			},
			want: "/besteffort/pod111-222-333",
		},
		{
			name: "Burstable",
			args: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: "111-222-333",
				},
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSBurstable,
				},
			},
			want: "/burstable/pod111-222-333",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filepath.Join("/", genPodCgroupParentDir(tt.args))
			if tt.want != got {
				t.Errorf("genPodCgroupParentDir want %v but got %v", tt.want, got)
			}
		})
	}
}

func Test_metaService_syncNode(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test",
			Labels: map[string]string{},
		},
	}

	m := statesInformer{}
	metrics.Register(testingNode)
	defer metrics.Register(nil)

	m.syncNode(testingNode)
}

// TODO: fix data race, https://github.com/koordinator-sh/koordinator/issues/77

// type testKubeletStub struct {
// 	pods corev1.PodList
// }
//
// func (t *testKubeletStub) GetAllPods() (corev1.PodList, error) {
// 	return t.pods, nil
// }
//
// func Test_metaService_syncPods(t *testing.T) {
// 	client := clientsetfake.NewSimpleClientset()
// 	pleg, _ := pleg.NewPLEG(sysutil.Conf.CgroupRootDir)
// 	stopCh := make(chan struct{})
// 	defer close(stopCh)
//
// 	c := NewDefaultConfig()
// 	c.KubeletSyncIntervalSeconds = 60
// 	m := NewStatesInformer(c, client, pleg, "localhost")
// 	m.(*statesInformer).kubelet = &testKubeletStub{pods: corev1.PodList{
// 		Items: []corev1.Pod{
// 			{},
// 		},
// 	}}
//
// 	go m.Run(stopCh)
//
// 	m.(*statesInformer).podCreated <- "pod1"
// 	time.Sleep(200 * time.Millisecond)
// 	if time.Since(m.(*statesInformer).podUpdatedTime) > time.Second {
// 		t.Errorf("failed to triggle update by pod created event")
// 	}
// 	if len(m.(*statesInformer).podMap) != 1 {
// 		t.Errorf("failed to update pods")
// 	}
// }
