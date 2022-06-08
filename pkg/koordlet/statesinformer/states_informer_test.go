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
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	koordclientfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/pleg"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
)

func Test_genPodCgroupParentDirWithSystemdDriver(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Systemd)
	defer system.SetupCgroupPathFormatter(system.Systemd)
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
	system.SetupCgroupPathFormatter(system.Cgroupfs)
	defer system.SetupCgroupPathFormatter(system.Systemd)
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

func Test_statesInformer_syncNode(t *testing.T) {
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

type testKubeletStub struct {
	pods corev1.PodList
}

func (t *testKubeletStub) GetAllPods() (corev1.PodList, error) {
	return t.pods, nil
}

type testErrorKubeletStub struct {
}

func (t *testErrorKubeletStub) GetAllPods() (corev1.PodList, error) {
	return corev1.PodList{}, errors.New("test error")
}

func Test_statesInformer_syncPods(t *testing.T) {
	client := clientsetfake.NewSimpleClientset()
	crdClient := koordclientfake.NewSimpleClientset()
	pleg, _ := pleg.NewPLEG(system.Conf.CgroupRootDir)
	stopCh := make(chan struct{}, 1)
	defer close(stopCh)
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test",
			Labels: map[string]string{},
		},
	}
	c := NewDefaultConfig()
	c.KubeletSyncIntervalSeconds = 60
	m := NewStatesInformer(c, client, crdClient, pleg, "localhost")
	m.(*statesInformer).node = testingNode
	m.(*statesInformer).kubelet = &testKubeletStub{pods: corev1.PodList{
		Items: []corev1.Pod{
			{},
		},
	}}

	m.(*statesInformer).syncPods()
	if len(m.(*statesInformer).GetAllPods()) != 1 {
		t.Fatal("failed to update pods")
	}

	m.(*statesInformer).kubelet = &testErrorKubeletStub{}

	err := m.(*statesInformer).syncPods()
	if err == nil {
		t.Fatalf("need not nil error, but get error %+v", err)
	}
}

func Test_newKubeletStub(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			DaemonEndpoints: corev1.NodeDaemonEndpoints{
				KubeletEndpoint: corev1.DaemonEndpoint{
					Port: 10250,
				},
			},
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "127.0.0.1"},
			},
		},
	}
	tokenContent := "test_token"
	f, err := ioutil.TempFile("", "token")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(tokenContent)
	kubeStub, _ := NewKubeletStub("127.0.0.7", 10250, 10, tokenContent)
	type args struct {
		node             *corev1.Node
		addressPreferred string
		timeout          int
		tokenPath        string
	}
	tests := []struct {
		name    string
		args    args
		want    KubeletStub
		wantErr bool
	}{
		{
			name: "NodeInternalIP",
			args: args{
				node:             testingNode,
				addressPreferred: string(corev1.NodeInternalIP),
				timeout:          10,
				tokenPath:        f.Name(),
			},
			want:    kubeStub,
			wantErr: false,
		},
		{
			name: "Empty IP",
			args: args{
				node:             testingNode,
				addressPreferred: "",
				timeout:          10,
				tokenPath:        f.Name(),
			},
			want:    kubeStub,
			wantErr: false,
		},
		{
			name: "Error Path",
			args: args{
				node:             testingNode,
				addressPreferred: "",
				timeout:          10,
				tokenPath:        "",
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newKubeletStub(tt.args.node, tt.args.addressPreferred, tt.args.timeout, tt.args.tokenPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("newKubeletStub() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && got != nil {
				t.Errorf("newKubeletStub() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_statesInformer_syncKubeletLoop(t *testing.T) {
	client := clientsetfake.NewSimpleClientset()
	crdClient := koordclientfake.NewSimpleClientset()
	pleg, _ := pleg.NewPLEG(system.Conf.CgroupRootDir)
	stopCh := make(chan struct{}, 1)

	c := NewDefaultConfig()
	c.KubeletSyncIntervalSeconds = 3

	m := NewStatesInformer(c, client, crdClient, pleg, "localhost")
	m.(*statesInformer).kubelet = &testKubeletStub{pods: corev1.PodList{
		Items: []corev1.Pod{
			{},
		},
	}}
	go m.(*statesInformer).syncKubeletLoop(time.Second*time.Duration(c.KubeletSyncIntervalSeconds), stopCh)
	time.Sleep(5 * time.Second)
	close(stopCh)
}
