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

package extendedresourcee

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestRule(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		r := newRule()
		assert.NotNil(t, r)

		// check the defaults
		got, got1 := r.GetCFSQuotaScaleRatio()
		assert.True(t, got)
		assert.Equal(t, float64(-1), got1)

		isUpdated := r.UpdateCFSQuotaEnabled(true)
		assert.True(t, isUpdated)
		got, _ = r.GetCFSQuotaScaleRatio()
		assert.True(t, got)
		isUpdated = r.UpdateCFSQuotaEnabled(true)
		assert.False(t, isUpdated)
		isUpdated = r.UpdateCFSQuotaEnabled(false)
		assert.True(t, isUpdated)
		got, _ = r.GetCFSQuotaScaleRatio()
		assert.False(t, got)
		isUpdated = r.UpdateCFSQuotaEnabled(true)
		assert.True(t, isUpdated)
		got, _ = r.GetCFSQuotaScaleRatio()
		assert.True(t, got)

		isUpdated = r.UpdateCPUNormalizationRatio(-1)
		assert.True(t, isUpdated)
		_, got1 = r.GetCFSQuotaScaleRatio()
		assert.Equal(t, float64(-1), got1)
		isUpdated = r.UpdateCPUNormalizationRatio(-1)
		assert.False(t, isUpdated)
		isUpdated = r.UpdateCPUNormalizationRatio(1.2)
		assert.True(t, isUpdated)
		_, got1 = r.GetCFSQuotaScaleRatio()
		assert.Equal(t, 1.2, got1)
	})
}

func Test_plugin_parseRuleForNodeSLO(t *testing.T) {
	type fields struct {
		rule *Rule
	}
	type args struct {
		mergedNodeSLO *slov1alpha1.NodeSLOSpec
	}
	tests := []struct {
		name     string
		fields   fields
		args     args
		want     bool
		wantErr  bool
		wantRule *Rule
	}{
		{
			name: "initialize true",
			fields: fields{
				rule: newRule(),
			},
			want:    true,
			wantErr: false,
			wantRule: &Rule{
				enableCFSQuota:        pointer.Bool(true),
				cpuNormalizationRatio: nil,
			},
		},
		{
			name: "initialize false",
			fields: fields{
				rule: newRule(),
			},
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
						Enable:            pointer.Bool(true),
						CPUSuppressPolicy: slov1alpha1.CPUCfsQuotaPolicy,
					},
				},
			},
			want:    true,
			wantErr: false,
			wantRule: &Rule{
				enableCFSQuota:        pointer.Bool(false),
				cpuNormalizationRatio: nil,
			},
		},
		{
			name: "rule change to false",
			fields: fields{
				rule: &Rule{
					enableCFSQuota:        pointer.Bool(true),
					cpuNormalizationRatio: pointer.Float64(-1),
				},
			},
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
						Enable:            pointer.Bool(true),
						CPUSuppressPolicy: slov1alpha1.CPUCfsQuotaPolicy,
					},
				},
			},
			want:    true,
			wantErr: false,
			wantRule: &Rule{
				enableCFSQuota:        pointer.Bool(false),
				cpuNormalizationRatio: pointer.Float64(-1),
			},
		},
		{
			name: "rule change to true",
			fields: fields{
				rule: &Rule{
					enableCFSQuota:        pointer.Bool(false),
					cpuNormalizationRatio: pointer.Float64(-1),
				},
			},
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{},
			},
			want:    true,
			wantErr: false,
			wantRule: &Rule{
				enableCFSQuota:        pointer.Bool(true),
				cpuNormalizationRatio: pointer.Float64(-1),
			},
		},
		{
			name: "rule not change as true",
			fields: fields{
				rule: &Rule{
					enableCFSQuota:        pointer.Bool(true),
					cpuNormalizationRatio: pointer.Float64(-1),
				},
			},
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{},
			},
			want:    false,
			wantErr: false,
			wantRule: &Rule{
				enableCFSQuota:        pointer.Bool(true),
				cpuNormalizationRatio: pointer.Float64(-1),
			},
		},
		{
			name: "rule not change as false",
			fields: fields{
				rule: &Rule{
					enableCFSQuota:        pointer.Bool(false),
					cpuNormalizationRatio: pointer.Float64(-1),
				},
			},
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
						Enable:            pointer.Bool(true),
						CPUSuppressPolicy: slov1alpha1.CPUCfsQuotaPolicy,
					},
				},
			},
			want:    false,
			wantErr: false,
			wantRule: &Rule{
				enableCFSQuota:        pointer.Bool(false),
				cpuNormalizationRatio: pointer.Float64(-1),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := plugin{
				rule: tt.fields.rule,
			}
			got, err := p.parseRuleForNodeSLO(tt.args.mergedNodeSLO)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantRule, p.rule)
		})
	}
}

func Test_plugin_ruleUpdateCbForNodeSLO(t *testing.T) {
	testSpec := &apiext.ExtendedResourceSpec{
		Containers: map[string]apiext.ExtendedResourceContainerSpec{
			"container-0": {
				Requests: corev1.ResourceList{
					apiext.BatchCPU:    resource.MustParse("500"),
					apiext.BatchMemory: resource.MustParse("2Gi"),
				},
				Limits: corev1.ResourceList{
					apiext.BatchCPU:    resource.MustParse("500"),
					apiext.BatchMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	testSpecBytes, err := json.Marshal(testSpec)
	assert.NoError(t, err)
	type fields struct {
		rule *Rule
	}
	type args struct {
		pods []*statesinformer.PodMeta
	}
	type want struct {
		cfsQuota string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		want    []want
	}{
		{
			name:    "rule is nil",
			wantErr: false,
			want: []want{
				{
					cfsQuota: "-1",
				},
			},
		},
		{
			name: "update no Batch pods",
			fields: fields{
				rule: &Rule{
					enableCFSQuota:        pointer.Bool(true),
					cpuNormalizationRatio: nil,
				},
			},
			args: args{
				pods: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								UID: "123456",
							},
							Status: corev1.PodStatus{
								ContainerStatuses: []corev1.ContainerStatus{
									{
										ContainerID: "docker://abcdef",
									},
								},
							},
						},
						CgroupDir: "/kubepods-besteffort.slice/kubepods-besteffort-pod123456.slice/",
					},
				},
			},
			wantErr: false,
			want: []want{
				{
					cfsQuota: "-1",
				},
			},
		},
		{
			name: "update a Batch pods",
			fields: fields{
				rule: &Rule{
					enableCFSQuota:        pointer.Bool(true),
					cpuNormalizationRatio: nil,
				},
			},
			args: args{
				pods: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								UID: "123456",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSBE),
								},
								Annotations: map[string]string{
									apiext.AnnotationExtendedResourceSpec: string(testSpecBytes),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "container-0",
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("500"),
												apiext.BatchMemory: resource.MustParse("2Gi"),
											},
											Limits: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("500"),
												apiext.BatchMemory: resource.MustParse("2Gi"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "container-0",
										ContainerID: "docker://abcdef",
									},
								},
							},
						},
						CgroupDir: "/kubepods-besteffort.slice/kubepods-besteffort-pod123456.slice/",
					},
				},
			},
			wantErr: false,
			want: []want{
				{
					cfsQuota: "50000",
				},
			},
		},
		{
			name: "update a Batch pods for unset cfs quota",
			fields: fields{
				rule: &Rule{
					enableCFSQuota:        pointer.Bool(false),
					cpuNormalizationRatio: nil,
				},
			},
			args: args{
				pods: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								UID: "123456",
								Labels: map[string]string{
									apiext.LabelPodQoS: string(apiext.QoSBE),
								},
								Annotations: map[string]string{
									apiext.AnnotationExtendedResourceSpec: string(testSpecBytes),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "container-0",
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("500"),
												apiext.BatchMemory: resource.MustParse("2Gi"),
											},
											Limits: corev1.ResourceList{
												apiext.BatchCPU:    resource.MustParse("500"),
												apiext.BatchMemory: resource.MustParse("2Gi"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "container-0",
										ContainerID: "docker://abcdef",
									},
								},
							},
						},
						CgroupDir: "/kubepods-besteffort.slice/kubepods-besteffort-pod123456.slice/",
					},
				},
			},
			wantErr: false,
			want: []want{
				{
					cfsQuota: "-1",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			// init cgroups cpuset file
			for _, podMeta := range tt.args.pods {
				cgroupDir := podMeta.CgroupDir
				helper.WriteCgroupFileContents(cgroupDir, sysutil.CPUCFSQuota, "-1")
				for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
					cgroupDir, err := util.GetContainerCgroupParentDirByID(podMeta.CgroupDir, containerStat.ContainerID)
					assert.NoError(t, err, "container "+containerStat.Name)
					helper.WriteCgroupFileContents(cgroupDir, sysutil.CPUCFSQuota, "-1")
				}
			}

			p := plugin{
				rule:     tt.fields.rule,
				executor: resourceexecutor.NewResourceUpdateExecutor(),
			}
			stop := make(chan struct{})
			defer func() { close(stop) }()
			p.executor.Run(stop)

			err := p.ruleUpdateCbForNodeSLO(&statesinformer.CallbackTarget{Pods: tt.args.pods})
			assert.Equal(t, tt.wantErr, err != nil)
			// init cgroups cpuset file
			for i, podMeta := range tt.args.pods {
				w := tt.want[i]
				cgroupDir := podMeta.CgroupDir
				assert.Equal(t, w.cfsQuota, helper.ReadCgroupFileContents(cgroupDir, sysutil.CPUCFSQuota))
				for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
					cgroupDir, err := util.GetContainerCgroupParentDirByID(podMeta.CgroupDir, containerStat.ContainerID)
					assert.NoError(t, err, "container "+containerStat.Name)
					assert.Equal(t, w.cfsQuota, helper.ReadCgroupFileContents(cgroupDir, sysutil.CPUCFSQuota))
				}
			}
		})
	}
}

func Test_plugin_parseRuleForNodeMeta(t *testing.T) {
	tests := []struct {
		name       string
		field      *Rule
		arg        interface{}
		want       bool
		wantErr    bool
		wantField  bool
		wantField1 float64
	}{
		{
			name:       "got invalid type of input",
			arg:        &slov1alpha1.NodeSLOSpec{},
			want:       false,
			wantErr:    true,
			wantField:  true,
			wantField1: -1,
		},
		{
			name:       "got nil input",
			arg:        (*corev1.Node)(nil),
			want:       false,
			wantErr:    true,
			wantField:  true,
			wantField1: -1,
		},
		{
			name: "parse ratio failed",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						apiext.AnnotationCPUNormalizationRatio: "[}",
					},
				},
			},
			want:       false,
			wantErr:    true,
			wantField:  true,
			wantField1: -1,
		},
		{
			name: "update new ratio",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						apiext.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
			want:       true,
			wantErr:    false,
			wantField:  true,
			wantField1: 1.1,
		},
		{
			name: "ratio not changed",
			field: &Rule{
				cpuNormalizationRatio: pointer.Float64(1.1),
			},
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						apiext.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
			want:       false,
			wantErr:    false,
			wantField:  true,
			wantField1: 1.1,
		},
		{
			name: "cfs quota disabled",
			field: &Rule{
				enableCFSQuota:        pointer.Bool(false),
				cpuNormalizationRatio: pointer.Float64(1.1),
			},
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						apiext.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
			want:       false,
			wantErr:    false,
			wantField:  false,
			wantField1: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			if tt.field != nil {
				p.rule = tt.field
			}
			got, gotErr := p.parseRuleForNodeMeta(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			gotCFSQuotaEnabled, gotRatio := p.rule.GetCFSQuotaScaleRatio()
			assert.Equal(t, tt.wantField, gotCFSQuotaEnabled)
			assert.Equal(t, tt.wantField1, gotRatio)
		})
	}
}

func Test_plugin_ruleUpdateCbForNodeMeta(t *testing.T) {
	type fields struct {
		rule    *Rule
		prepare func(t *testing.T, helper *sysutil.FileTestUtil)
	}
	tests := []struct {
		name      string
		fields    fields
		arg       []*statesinformer.PodMeta
		wantErr   bool
		wantCheck func(t *testing.T, helper *sysutil.FileTestUtil)
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
					enableCFSQuota:        pointer.Bool(true),
					cpuNormalizationRatio: pointer.Float64(1.1),
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
					enableCFSQuota:        pointer.Bool(true),
					cpuNormalizationRatio: pointer.Float64(1.2),
				},
				prepare: func(t *testing.T, helper *sysutil.FileTestUtil) {
					sysutil.SetupCgroupPathFormatter(sysutil.Systemd)
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-podabc123.slice", sysutil.CPUCFSQuota, "100000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-podabc123.slice", sysutil.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUCFSQuota, "100000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUShares, "1024")
				},
			},
			arg: []*statesinformer.PodMeta{
				{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-lse-pod",
							Labels: map[string]string{
								apiext.LabelPodQoS: string(apiext.QoSLSE),
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodPending,
						},
					},
				},
				{
					CgroupDir: "/kubepods.slice/kubepods-besteffort.slice/kubepods-podabc123.slice/",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-be-pod",
							UID:  "abc123",
							Labels: map[string]string{
								apiext.LabelPodQoS: string(apiext.QoSBE),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-be-container",
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
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-be-container",
									ContainerID: "containerd://testxxx",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantCheck: func(t *testing.T, helper *sysutil.FileTestUtil) {
				podCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-podabc123.slice", sysutil.CPUCFSQuota)
				assert.Equal(t, "83334", podCFSQuota)
				podCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-podabc123.slice", sysutil.CPUShares)
				assert.Equal(t, "1024", podCPUShares)
				containerCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUCFSQuota)
				assert.Equal(t, "83334", containerCFSQuota)
				containerCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUShares)
				assert.Equal(t, "1024", containerCPUShares)
			},
		},
		{
			name: "reconcile a pod with small ratio",
			fields: fields{
				rule: &Rule{
					enableCFSQuota:        pointer.Bool(true),
					cpuNormalizationRatio: pointer.Float64(1.0), // previous 1.2
				},
				prepare: func(t *testing.T, helper *sysutil.FileTestUtil) {
					sysutil.SetupCgroupPathFormatter(sysutil.Systemd)
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.CPUCFSQuota, "83334")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.MemoryLimit, "1073741824")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUCFSQuota, "83334")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.MemoryLimit, "1073741824")
				},
			},
			arg: []*statesinformer.PodMeta{
				{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-lse-pod",
							Labels: map[string]string{
								apiext.LabelPodQoS: string(apiext.QoSLSE),
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodPending,
						},
					},
				},
				{
					CgroupDir: "/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-be-pod",
							UID:  "abc123",
							Labels: map[string]string{
								apiext.LabelPodQoS: string(apiext.QoSBE),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-be-container",
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
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-be-container",
									ContainerID: "containerd://testxxx",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantCheck: func(t *testing.T, helper *sysutil.FileTestUtil) {
				podCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.CPUCFSQuota)
				assert.Equal(t, "100000", podCFSQuota)
				podCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.CPUShares)
				assert.Equal(t, "1024", podCPUShares)
				podMemoryLimit := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.MemoryLimit)
				assert.Equal(t, "1073741824", podMemoryLimit)
				containerCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUCFSQuota)
				assert.Equal(t, "100000", containerCFSQuota)
				containerCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUShares)
				assert.Equal(t, "1024", containerCPUShares)
				containerMemoryLimit := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.MemoryLimit)
				assert.Equal(t, "1073741824", containerMemoryLimit)
			},
		},
		{
			name: "reconcile multi pods",
			fields: fields{
				rule: &Rule{
					enableCFSQuota:        pointer.Bool(true),
					cpuNormalizationRatio: pointer.Float64(1.2),
				},
				prepare: func(t *testing.T, helper *sysutil.FileTestUtil) {
					sysutil.SetupCgroupPathFormatter(sysutil.Systemd)
					// test-be-pod
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.CPUCFSQuota, "100000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.MemoryLimit, "1073741824")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUCFSQuota, "100000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUShares, "1024")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.MemoryLimit, "1073741824")
					// test-be-pod-1
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", sysutil.CPUCFSQuota, "200000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", sysutil.CPUShares, "2048")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", sysutil.MemoryLimit, "2147483648")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", sysutil.CPUCFSQuota, "200000")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", sysutil.CPUShares, "2048")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", sysutil.MemoryLimit, "2147483648")
				},
			},
			arg: []*statesinformer.PodMeta{
				{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-lse-pod",
							Labels: map[string]string{
								apiext.LabelPodQoS: string(apiext.QoSLSE),
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodPending,
						},
					},
				},
				{
					CgroupDir: "/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-be-pod",
							UID:  "abc123",
							Labels: map[string]string{
								apiext.LabelPodQoS: string(apiext.QoSBE),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-be-container",
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
							Phase: corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-be-container",
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
							Name: "test-none-pod-1",
							UID:  "xyz456",
							Labels: map[string]string{
								apiext.LabelPodQoS: string(apiext.QoSNone),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-none-container-1",
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
									Name:        "test-none-container-1",
									ContainerID: "containerd://testyyy",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantCheck: func(t *testing.T, helper *sysutil.FileTestUtil) {
				podCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.CPUCFSQuota)
				assert.Equal(t, "83334", podCFSQuota)
				podCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.CPUShares)
				assert.Equal(t, "1024", podCPUShares)
				podMemoryLimit := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice", sysutil.MemoryLimit)
				assert.Equal(t, "1073741824", podMemoryLimit)
				containerCFSQuota := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUCFSQuota)
				assert.Equal(t, "83334", containerCFSQuota)
				containerCPUShares := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.CPUShares)
				assert.Equal(t, "1024", containerCPUShares)
				containerMemoryLimit := helper.ReadCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc123.slice/cri-containerd-testxxx.scope", sysutil.MemoryLimit)
				assert.Equal(t, "1073741824", containerMemoryLimit)
				podCFSQuota1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", sysutil.CPUCFSQuota)
				assert.Equal(t, "200000", podCFSQuota1)
				podCPUShares1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", sysutil.CPUShares)
				assert.Equal(t, "2048", podCPUShares1)
				podMemoryLimit1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice", sysutil.MemoryLimit)
				assert.Equal(t, "2147483648", podMemoryLimit1)
				containerCFSQuota1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", sysutil.CPUCFSQuota)
				assert.Equal(t, "200000", containerCFSQuota1)
				containerCPUShares1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", sysutil.CPUShares)
				assert.Equal(t, "2048", containerCPUShares1)
				containerMemoryLimit1 := helper.ReadCgroupFileContents("kubepods.slice/kubepods-podxyz456.slice/cri-containerd-testyyy.scope", sysutil.MemoryLimit)
				assert.Equal(t, "2147483648", containerMemoryLimit1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
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
			target := &statesinformer.CallbackTarget{
				Pods: tt.arg,
			}
			gotErr := p.ruleUpdateCbForNodeMeta(target)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			if tt.wantCheck != nil {
				tt.wantCheck(t, helper)
			}
		})
	}
}
