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

package cpunormalization

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestRule(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		r := newRule()
		enabled := r.IsEnabled()
		assert.False(t, enabled)
		ratio := r.GetCPUNormalizationRatio()
		assert.Equal(t, float64(-1), ratio)
		isUpdated := r.UpdateRule(1.1)
		assert.True(t, isUpdated)
		enabled = r.IsEnabled()
		assert.True(t, enabled)
		ratio = r.GetCPUNormalizationRatio()
		assert.Equal(t, 1.1, ratio)
		isUpdated = r.UpdateRule(1.1)
		assert.False(t, isUpdated)
	})
}

func TestPlugin_parseRule(t *testing.T) {
	tests := []struct {
		name      string
		field     *float64
		arg       interface{}
		want      bool
		wantErr   bool
		wantField float64
	}{
		{
			name:      "got invalid type of input",
			arg:       &slov1alpha1.NodeSLOSpec{},
			want:      false,
			wantErr:   true,
			wantField: -1,
		},
		{
			name:      "got nil input",
			arg:       (*corev1.Node)(nil),
			want:      false,
			wantErr:   true,
			wantField: -1,
		},
		{
			name: "parse ratio failed",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "[}",
					},
				},
			},
			want:      false,
			wantErr:   true,
			wantField: -1,
		},
		{
			name: "update new ratio",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
			want:      true,
			wantErr:   false,
			wantField: 1.1,
		},
		{
			name:  "ratio not changed",
			field: pointer.Float64(1.1),
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
			want:      false,
			wantErr:   false,
			wantField: 1.1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			if tt.field != nil {
				p.rule.UpdateRule(*tt.field)
			}
			got, gotErr := p.parseRule(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			gotRatio := p.rule.GetCPUNormalizationRatio()
			assert.Equal(t, tt.wantField, gotRatio)
		})
	}
}

func TestPlugin_ruleUpdateCb(t *testing.T) {
	type fields struct {
		rule    *Rule
		prepare func(t *testing.T, helper *system.FileTestUtil)
	}
	tests := []struct {
		name      string
		fields    fields
		arg       []*statesinformer.PodMeta
		wantErr   bool
		wantCheck func(t *testing.T, helper *system.FileTestUtil)
	}{
		{
			name: "rule is uninitialized",
			fields: fields{
				rule: nil,
			},
			wantErr: false,
		},
		{
			name: "no matched pod to reconcile",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 1.1,
				},
			},
			arg: []*statesinformer.PodMeta{
				{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-terminated-pod",
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodFailed,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "reconcile a pod with larger ratio",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 1.2,
				},
				prepare: func(t *testing.T, helper *system.FileTestUtil) {
					system.SetupCgroupPathFormatter(system.Systemd)
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUCFSQuota, "100000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUCFSQuota, "100000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUShares, "1024")
				},
			},
			arg: []*statesinformer.PodMeta{
				{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-lse-pod",
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSE),
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodPending,
						},
					},
				},
				{
					CgroupDir: "/kubepods.slice/kubepods-podabc123.slice/",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-ls-pod",
							UID:  "abc123",
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLS),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-ls-container",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-ls-container",
									ContainerID: "containerd://testxxx",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantCheck: func(t *testing.T, helper *system.FileTestUtil) {
				podCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUCFSQuota)
				assert.Equal(t, "83334", podCFSQuota)
				podCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUShares)
				assert.Equal(t, "1024", podCPUShares)
				containerCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUCFSQuota)
				assert.Equal(t, "83334", containerCFSQuota)
				containerCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUShares)
				assert.Equal(t, "1024", containerCPUShares)
			},
		},
		{
			name: "reconcile a pod with small ratio",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 1.0, // previous 1.2
				},
				prepare: func(t *testing.T, helper *system.FileTestUtil) {
					system.SetupCgroupPathFormatter(system.Systemd)
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUCFSQuota, "83334")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.MemoryLimit, "1073741824")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUCFSQuota, "83334")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.MemoryLimit, "1073741824")
				},
			},
			arg: []*statesinformer.PodMeta{
				{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-lse-pod",
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSE),
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodPending,
						},
					},
				},
				{
					CgroupDir: "/kubepods.slice/kubepods-podabc123.slice/",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-ls-pod",
							UID:  "abc123",
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLS),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-ls-container",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-ls-container",
									ContainerID: "containerd://testxxx",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantCheck: func(t *testing.T, helper *system.FileTestUtil) {
				podCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUCFSQuota)
				assert.Equal(t, "100000", podCFSQuota)
				podCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUShares)
				assert.Equal(t, "1024", podCPUShares)
				podMemoryLimit := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.MemoryLimit)
				assert.Equal(t, "1073741824", podMemoryLimit)
				containerCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUCFSQuota)
				assert.Equal(t, "100000", containerCFSQuota)
				containerCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUShares)
				assert.Equal(t, "1024", containerCPUShares)
				containerMemoryLimit := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.MemoryLimit)
				assert.Equal(t, "1073741824", containerMemoryLimit)
			},
		},
		{
			name: "reconcile multi pods",
			fields: fields{
				rule: &Rule{
					enable:   true,
					curRatio: 1.2,
				},
				prepare: func(t *testing.T, helper *system.FileTestUtil) {
					system.SetupCgroupPathFormatter(system.Systemd)
					// test-ls-pod
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUCFSQuota, "100000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.MemoryLimit, "1073741824")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUCFSQuota, "100000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.MemoryLimit, "1073741824")
					// test-ls-none-pod
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", system.CPUCFSQuota, "200000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", system.CPUShares, "2048")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", system.MemoryLimit, "2147483648")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", system.CPUCFSQuota, "200000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", system.CPUShares, "2048")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", system.MemoryLimit, "2147483648")
				},
			},
			arg: []*statesinformer.PodMeta{
				{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-lse-pod",
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSE),
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodPending,
						},
					},
				},
				{
					CgroupDir: "/kubepods.slice/kubepods-podabc123.slice/",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-ls-pod",
							UID:  "abc123",
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLS),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-ls-container",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-ls-container",
									ContainerID: "containerd://testxxx",
								},
							},
						},
					},
				},
				{
					CgroupDir: "/kubepods.slice/kubepods-podxyz456.slice/",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-ls-none-pod",
							UID:  "xyz456",
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSNone),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-ls-none-container",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
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
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-ls-none-container",
									ContainerID: "containerd://testyyy",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantCheck: func(t *testing.T, helper *system.FileTestUtil) {
				podCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUCFSQuota)
				assert.Equal(t, "83334", podCFSQuota)
				podCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.CPUShares)
				assert.Equal(t, "1024", podCPUShares)
				podMemoryLimit := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice", system.MemoryLimit)
				assert.Equal(t, "1073741824", podMemoryLimit)
				containerCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUCFSQuota)
				assert.Equal(t, "83334", containerCFSQuota)
				containerCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.CPUShares)
				assert.Equal(t, "1024", containerCPUShares)
				containerMemoryLimit := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", system.MemoryLimit)
				assert.Equal(t, "1073741824", containerMemoryLimit)
				podCFSQuota1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", system.CPUCFSQuota)
				assert.Equal(t, "166667", podCFSQuota1)
				podCPUShares1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", system.CPUShares)
				assert.Equal(t, "2048", podCPUShares1)
				podMemoryLimit1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", system.MemoryLimit)
				assert.Equal(t, "2147483648", podMemoryLimit1)
				containerCFSQuota1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", system.CPUCFSQuota)
				assert.Equal(t, "166667", containerCFSQuota1)
				containerCPUShares1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", system.CPUShares)
				assert.Equal(t, "2048", containerCPUShares1)
				containerMemoryLimit1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", system.MemoryLimit)
				assert.Equal(t, "2147483648", containerMemoryLimit1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			stopCh := make(chan struct{})
			defer close(stopCh)
			if tt.fields.prepare != nil {
				tt.fields.prepare(t, helper)
			}
			p := newPlugin()
			p.executor = resourceexecutor.NewTestResourceExecutor()
			p.executor.Run(stopCh)
			p.rule = tt.fields.rule
			gotErr := p.ruleUpdateCb(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			if tt.wantCheck != nil {
				tt.wantCheck(t, helper)
			}
		})
	}
}
