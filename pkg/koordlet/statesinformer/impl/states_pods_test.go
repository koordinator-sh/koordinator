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

package impl

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	faketopologyclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	fakekoordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

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
			want: "/kubepods/pod111-222-333",
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
			want: "/kubepods/besteffort/pod111-222-333",
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
			want: "/kubepods/burstable/pod111-222-333",
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

type testKubeletStub struct {
	pods   corev1.PodList
	config *kubeletconfiginternal.KubeletConfiguration
}

func (t *testKubeletStub) GetAllPods() (corev1.PodList, error) {
	return t.pods, nil
}

func (t *testKubeletStub) GetKubeletConfiguration() (*kubeletconfiginternal.KubeletConfiguration, error) {
	return t.config, nil
}

type testErrorKubeletStub struct {
}

func (t *testErrorKubeletStub) GetAllPods() (corev1.PodList, error) {
	return corev1.PodList{}, errors.New("test error")
}

func (t *testErrorKubeletStub) GetKubeletConfiguration() (*kubeletconfiginternal.KubeletConfiguration, error) {
	return nil, errors.New("test error")
}

func Test_podsInformer(t *testing.T) {
	tempCgroupDir := t.TempDir()
	type fields struct {
		GetCgroupDirFn func(helper *system.FileTestUtil) string
	}
	type args struct {
		ctx    *PluginOption
		states *PluginState
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "default config",
			fields: fields{
				GetCgroupDirFn: nil,
			},
			args: args{
				ctx: &PluginOption{
					config:      NewDefaultConfig(),
					KubeClient:  fakeclientset.NewSimpleClientset(),
					KoordClient: fakekoordclientset.NewSimpleClientset(),
					TopoClient:  faketopologyclientset.NewSimpleClientset(),
					NodeName:    "test-node",
				},
				states: &PluginState{
					informerPlugins: map[PluginName]informerPlugin{
						nodeInformerName: NewNodeInformer(),
					},
				},
			},
		},
		{
			name: "change cgroup root",
			fields: fields{
				GetCgroupDirFn: func(helper *system.FileTestUtil) string {
					return tempCgroupDir
				},
			},
			args: args{
				ctx: &PluginOption{
					config:      NewDefaultConfig(),
					KubeClient:  fakeclientset.NewSimpleClientset(),
					KoordClient: fakekoordclientset.NewSimpleClientset(),
					TopoClient:  faketopologyclientset.NewSimpleClientset(),
					NodeName:    "test-node",
				},
				states: &PluginState{
					informerPlugins: map[PluginName]informerPlugin{
						nodeInformerName: NewNodeInformer(),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			oldCgroupRoot := system.Conf.CgroupRootDir
			if tt.fields.GetCgroupDirFn != nil {
				helper.SetConf(func(conf *system.Config) {
					conf.CgroupRootDir = tt.fields.GetCgroupDirFn(helper)
				}, func(conf *system.Config) {
					conf.CgroupRootDir = oldCgroupRoot
				})
			}
			defer helper.Cleanup()
			pi := NewPodsInformer()
			assert.NotNil(t, pi)
			assert.NotPanics(t, func() {
				pi.Setup(tt.args.ctx, tt.args.states)
			})
			assert.NotNil(t, pi.pleg)
		})
	}
}

func Test_statesInformer_syncPods(t *testing.T) {
	stopCh := make(chan struct{}, 1)
	defer close(stopCh)
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test",
			Labels: map[string]string{},
		},
	}
	c := NewDefaultConfig()
	c.KubeletSyncInterval = 60 * time.Second
	m := &podsInformer{
		nodeInformer: &nodeInformer{
			node: testingNode,
		},
		kubelet: &testKubeletStub{pods: corev1.PodList{
			Items: []corev1.Pod{
				{},
			},
		}},
		podHasSynced:   atomic.NewBool(false),
		callbackRunner: NewCallbackRunner(),
		cgroupReader:   resourceexecutor.NewCgroupReader(),
		config:         c,
	}

	err := m.syncPods()
	assert.NoError(t, err)
	if len(m.GetAllPods()) != 1 {
		t.Fatal("failed to update pods")
	}

	m.kubelet = &testErrorKubeletStub{}

	err = m.syncPods()
	assert.Error(t, err)
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

	dir := t.TempDir()
	cfg := &rest.Config{
		Host:        net.JoinHostPort("127.0.0.1", "10250"),
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
		},
	}
	setConfigs(t, dir)

	kubeStub, _ := NewKubeletStub("127.0.0.1", 10250, "https", 10, cfg)
	type args struct {
		node *corev1.Node
		cfg  *Config
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
				node: testingNode,
				cfg: &Config{
					KubeletPreferredAddressType: string(corev1.NodeInternalIP),
					KubeletSyncTimeout:          10 * time.Second,
					InsecureKubeletTLS:          true,
					KubeletReadOnlyPort:         10250,
				},
			},
			want:    kubeStub,
			wantErr: false,
		},
		{
			name: "Empty IP",
			args: args{
				node: testingNode,
				cfg: &Config{
					KubeletPreferredAddressType: "",
					KubeletSyncTimeout:          10 * time.Second,
					InsecureKubeletTLS:          true,
					KubeletReadOnlyPort:         10250,
				},
			},
			want:    kubeStub,
			wantErr: false,
		},
		{
			name: "HTTPS",
			args: args{
				node: testingNode,
				cfg: &Config{
					KubeletPreferredAddressType: "",
					KubeletSyncTimeout:          10 * time.Second,
					InsecureKubeletTLS:          false,
					KubeletReadOnlyPort:         10250,
				},
			},
			want:    nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newKubeletStubFromConfig(tt.args.node, tt.args.cfg)
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

func setConfigs(t *testing.T, dir string) {
	// Set KUBECONFIG env value
	kubeconfigEnvPath := filepath.Join(dir, "kubeconfig-text-context")
	err := os.WriteFile(kubeconfigEnvPath, []byte(genKubeconfig("from-env")), 0644)
	assert.NoError(t, err)
	t.Setenv(clientcmd.RecommendedConfigPathEnvVar, kubeconfigEnvPath)
}

func genKubeconfig(contexts ...string) string {
	var sb strings.Builder
	sb.WriteString("---\napiVersion: v1\nkind: Config\nclusters:\n")
	for _, ctx := range contexts {
		sb.WriteString("- cluster:\n    server: " + ctx + "\n  name: " + ctx + "\n")
	}
	sb.WriteString("contexts:\n")
	for _, ctx := range contexts {
		sb.WriteString("- context:\n    cluster: " + ctx + "\n    user: " + ctx + "\n  name: " + ctx + "\n")
	}

	sb.WriteString("users:\n")
	for _, ctx := range contexts {
		sb.WriteString("- name: " + ctx + "\n")
	}
	sb.WriteString("preferences: {}\n")
	if len(contexts) > 0 {
		sb.WriteString("current-context: " + contexts[0] + "\n")
	}
	return sb.String()
}

func Test_statesInformer_syncKubeletLoop(t *testing.T) {
	stopCh := make(chan struct{}, 1)

	c := NewDefaultConfig()
	c.KubeletSyncInterval = 3 * time.Second

	m := &podsInformer{
		kubelet: &testKubeletStub{pods: corev1.PodList{
			Items: []corev1.Pod{
				{},
			},
		}},
		callbackRunner: NewCallbackRunner(),
		podHasSynced:   atomic.NewBool(false),
		cgroupReader:   resourceexecutor.NewCgroupReader(),
		config:         c,
	}
	go m.syncKubeletLoop(c.KubeletSyncInterval, stopCh)
	time.Sleep(5 * time.Second)
	close(stopCh)
}

func Test_resetPodMetrics(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
		},
	}
	assert.NotPanics(t, func() {
		metrics.Register(testingNode)
		defer metrics.Register(nil)

		resetPodMetrics()
	})
}

func Test_recordPodResourceMetrics(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
		},
	}
	testingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test_pod",
			Namespace: "test_pod_namespace",
			UID:       "test01",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test_container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test_container",
					ContainerID: "containerd://testxxx",
				},
			},
		},
	}
	testingBatchPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test_batch_pod",
			Namespace: "test_batch_pod_namespace",
			UID:       "batch01",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test_batch_container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("1000"),
							apiext.BatchMemory: resource.MustParse("2Gi"),
						},
						Limits: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("1000"),
							apiext.BatchMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test_batch_container",
					ContainerID: "containerd://batchxxx",
				},
			},
		},
	}
	tests := []struct {
		name string
		arg  *statesinformer.PodMeta
	}{
		{
			name: "pod meta is invalid",
			arg:  nil,
		},
		{
			name: "pod meta is invalid 1",
			arg:  &statesinformer.PodMeta{},
		},
		{
			name: "record a normally pod",
			arg: &statesinformer.PodMeta{
				Pod: testingPod,
			},
		},
		{
			name: "record a batch pod",
			arg: &statesinformer.PodMeta{
				Pod: testingBatchPod,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics.Register(testingNode)
			defer metrics.Register(nil)

			recordPodResourceMetrics(tt.arg)
		})
	}
}

func testingPrepareContainerCgroupCPUTasks(t *testing.T, helper *system.FileTestUtil, containerParentPath, tasksStr string) {
	tasks, err := system.GetCgroupResource(system.CPUTasksName)
	assert.NoError(t, err)
	helper.WriteCgroupFileContents(containerParentPath, tasks, tasksStr)
}

func Test_getTaskIds(t *testing.T) {
	type args struct {
		podMeta *statesinformer.PodMeta
	}
	type fields struct {
		containerParentDir string
		containerTasksStr  string
		invalidPath        bool
		useCgroupsV2       bool
	}
	tests := []struct {
		name   string
		args   args
		fields fields
		want   map[string][]int32
	}{
		{
			name: "do nothing for empty pod",
			args: args{
				podMeta: &statesinformer.PodMeta{Pod: &corev1.Pod{}},
			},
			want: nil,
		},
		{
			name: "successfully get task ids for the pod",
			fields: fields{
				containerParentDir: "kubepods.slice/p0/cri-containerd-c0.scope",
				containerTasksStr:  "122450\n122454\n123111\n128912",
			},
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod0",
							UID:  "p0",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "container0",
								},
							},
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "container0",
									ContainerID: "containerd://c0",
								},
							},
						},
					},
					CgroupDir:        "kubepods.slice/p0",
					ContainerTaskIds: make(map[string][]int32),
				},
			},
			want: map[string][]int32{"containerd://c0": {122450, 122454, 123111, 128912}},
		},
		{
			name: "return empty for invalid path",
			fields: fields{
				containerParentDir: "p0/cri-containerd-c0.scope",
				containerTasksStr:  "122454\n123111\n128912",
				invalidPath:        true,
			},
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod0",
							UID:  "p0",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "container0",
								},
							},
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "container0",
									ContainerID: "containerd://c0",
								},
							},
						},
					},
					CgroupDir:        "kubepods.slice/p0",
					ContainerTaskIds: make(map[string][]int32),
				},
			},
			want: make(map[string][]int32),
		},
		{
			name: "missing container's status",
			fields: fields{
				containerParentDir: "p0/cri-containerd-c0.scope",
				containerTasksStr:  "122454\n123111\n128912",
				invalidPath:        true,
			},
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod0",
							UID:  "p0",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "container0",
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:             corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{},
						},
					},
					CgroupDir:        "kubepods.slice/p0",
					ContainerTaskIds: make(map[string][]int32),
				},
			},
			want: make(map[string][]int32),
		},
		{
			name: "successfully get task ids on cgroups v2",
			fields: fields{
				containerParentDir: "kubepods.slice/p0/cri-containerd-c0.scope",
				containerTasksStr:  "122450\n122454\n123111\n128912",
				useCgroupsV2:       true,
			},
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod0",
							UID:  "p0",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "container0",
								},
							},
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "container0",
									ContainerID: "containerd://c0",
								},
							},
						},
					},
					CgroupDir:        "kubepods.slice/p0",
					ContainerTaskIds: make(map[string][]int32),
				},
			},
			want: map[string][]int32{"containerd://c0": {122450, 122454, 123111, 128912}},
		},
		{
			name: "successfully get task ids from the sandbox container",
			fields: fields{
				containerParentDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podp0.slice/cri-containerd-abc.scope",
				containerTasksStr:  "122450\n122454\n123111\n128912",
				useCgroupsV2:       true,
			},
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod0",
							UID:  "p0",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "container0",
								},
							},
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "container0",
									ContainerID: "containerd://c0",
								},
							},
						},
					},
					CgroupDir:        "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podp0.slice",
					ContainerTaskIds: make(map[string][]int32),
				},
			},
			want: map[string][]int32{"containerd://abc": {122450, 122454, 123111, 128912}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			helper.SetCgroupsV2(tt.fields.useCgroupsV2)
			defer helper.Cleanup()

			testingPrepareContainerCgroupCPUTasks(t, helper, tt.fields.containerParentDir, tt.fields.containerTasksStr)

			system.CommonRootDir = ""
			if tt.fields.invalidPath {
				system.Conf.CgroupRootDir = "invalidPath"
			}

			c := NewDefaultConfig()

			m := &podsInformer{
				kubelet: &testKubeletStub{pods: corev1.PodList{
					Items: []corev1.Pod{
						{},
					},
				}},
				callbackRunner: NewCallbackRunner(),
				podHasSynced:   atomic.NewBool(false),
				cgroupReader:   resourceexecutor.NewCgroupReader(),
				config:         c,
			}

			m.getTaskIds(tt.args.podMeta)
			assert.Equal(t, tt.want, tt.args.podMeta.ContainerTaskIds)
		})
	}
}
