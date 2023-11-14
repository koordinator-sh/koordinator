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

package groupidentity

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_bvtRule_getPodBvtValue(t *testing.T) {
	type fields struct {
		podQOSParams     map[ext.QoSClass]int64
		kubeQOSDirParams map[corev1.PodQOSClass]int64
		kubeQOSPodParams map[corev1.PodQOSClass]int64
	}
	type args struct {
		podQoSClass ext.QoSClass
		podKubeQoS  corev1.PodQOSClass
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int64
	}{
		{
			name: "use koord qos",
			fields: fields{
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLS: 2,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSBurstable: 1,
				},
			},
			args: args{
				podQoSClass: ext.QoSLS,
				podKubeQoS:  corev1.PodQOSGuaranteed,
			},
			want: 2,
		},
		{
			name: "use kube qos",
			fields: fields{
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLS: 2,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSBurstable: 1,
				},
			},
			args: args{
				podQoSClass: ext.QoSNone,
				podKubeQoS:  corev1.PodQOSBurstable,
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &bvtRule{
				podQOSParams:     tt.fields.podQOSParams,
				kubeQOSDirParams: tt.fields.kubeQOSDirParams,
				kubeQOSPodParams: tt.fields.kubeQOSPodParams,
			}
			if got := r.getPodBvtValue(tt.args.podQoSClass, tt.args.podKubeQoS); got != tt.want {
				t.Errorf("getPodBvtValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_bvtPlugin_parseRule(t *testing.T) {
	policyGroupIdentity := slov1alpha1.CPUQOSPolicyGroupIdentity
	policyCoreSched := slov1alpha1.CPUQOSPolicyCoreSched
	type args struct {
		rule          *bvtRule
		mergedNodeSLO *slov1alpha1.NodeSLOSpec
	}
	tests := []struct {
		name     string
		args     args
		want     bool
		wantErr  bool
		wantRule bvtRule
	}{
		{
			name: "parse normal rules",
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						Policies: &slov1alpha1.ResourceQOSPolicies{
							CPUPolicy: &policyGroupIdentity,
						},
						LSRClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						LSClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(-1),
								},
							},
						},
					},
				},
			},
			wantRule: bvtRule{
				enable: true,
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLSE: 2,
					ext.QoSLSR: 2,
					ext.QoSLS:  2,
					ext.QoSBE:  -1,
				},
				kubeQOSDirParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  2,
					corev1.PodQOSBestEffort: -1,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 2,
					corev1.PodQOSBurstable:  2,
					corev1.PodQOSBestEffort: -1,
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "parse rules with lsr disabled",
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						Policies: &slov1alpha1.ResourceQOSPolicies{
							CPUPolicy: &policyGroupIdentity,
						},
						LSRClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(false),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(0),
								},
							},
						},
						LSClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(-1),
								},
							},
						},
					},
				},
			},
			wantRule: bvtRule{
				enable: true,
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLSE: 0,
					ext.QoSLSR: 0,
					ext.QoSLS:  2,
					ext.QoSBE:  -1,
				},
				kubeQOSDirParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  2,
					corev1.PodQOSBestEffort: -1,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 2,
					corev1.PodQOSBurstable:  2,
					corev1.PodQOSBestEffort: -1,
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "parse rules with lsr and ls disabled",
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						Policies: &slov1alpha1.ResourceQOSPolicies{
							CPUPolicy: &policyGroupIdentity,
						},
						LSRClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(false),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(0),
								},
							},
						},
						LSClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(false),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(0),
								},
							},
						},
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(-1),
								},
							},
						},
					},
				},
			},
			wantRule: bvtRule{
				enable: true,
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLSE: 0,
					ext.QoSLSR: 0,
					ext.QoSLS:  0,
					ext.QoSBE:  -1,
				},
				kubeQOSDirParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  0,
					corev1.PodQOSBestEffort: -1,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  0,
					corev1.PodQOSBestEffort: -1,
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "parse rules with all disabled",
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						Policies: &slov1alpha1.ResourceQOSPolicies{
							CPUPolicy: &policyGroupIdentity,
						},
						LSRClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(false),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(0),
								},
							},
						},
						LSClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(false),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(0),
								},
							},
						},
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(false),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(0),
								},
							},
						},
					},
				},
			},
			wantRule: bvtRule{
				enable: false,
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLSE: 0,
					ext.QoSLSR: 0,
					ext.QoSLS:  0,
					ext.QoSBE:  0,
				},
				kubeQOSDirParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  0,
					corev1.PodQOSBestEffort: 0,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  0,
					corev1.PodQOSBestEffort: 0,
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "parse same normal rules",
			args: args{
				rule: &bvtRule{
					enable: true,
					podQOSParams: map[ext.QoSClass]int64{
						ext.QoSLSE: 2,
						ext.QoSLSR: 2,
						ext.QoSLS:  2,
						ext.QoSBE:  -1,
					},
					kubeQOSDirParams: map[corev1.PodQOSClass]int64{
						corev1.PodQOSGuaranteed: 0,
						corev1.PodQOSBurstable:  2,
						corev1.PodQOSBestEffort: -1,
					},
					kubeQOSPodParams: map[corev1.PodQOSClass]int64{
						corev1.PodQOSGuaranteed: 2,
						corev1.PodQOSBurstable:  2,
						corev1.PodQOSBestEffort: -1,
					},
				},
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						Policies: &slov1alpha1.ResourceQOSPolicies{
							CPUPolicy: &policyGroupIdentity,
						},
						LSRClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						LSClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(-1),
								},
							},
						},
					},
				},
			},
			wantRule: bvtRule{
				enable: true,
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLSE: 2,
					ext.QoSLSR: 2,
					ext.QoSLS:  2,
					ext.QoSBE:  -1,
				},
				kubeQOSDirParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  2,
					corev1.PodQOSBestEffort: -1,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 2,
					corev1.PodQOSBurstable:  2,
					corev1.PodQOSBestEffort: -1,
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "only enable cpu qos for BE",
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						Policies: &slov1alpha1.ResourceQOSPolicies{
							CPUPolicy: &policyGroupIdentity,
						},
						LSRClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(false),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						LSClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(false),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(-1),
								},
							},
						},
					},
				},
			},
			wantRule: bvtRule{
				enable: true,
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLSE: 0,
					ext.QoSLSR: 0,
					ext.QoSLS:  0,
					ext.QoSBE:  -1,
				},
				kubeQOSDirParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  0,
					corev1.PodQOSBestEffort: -1,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  0,
					corev1.PodQOSBestEffort: -1,
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "parse default cpu qos policy",
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						Policies: &slov1alpha1.ResourceQOSPolicies{},
						LSRClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						LSClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(-1),
								},
							},
						},
					},
				},
			},
			wantRule: bvtRule{
				enable: true,
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLSE: 2,
					ext.QoSLSR: 2,
					ext.QoSLS:  2,
					ext.QoSBE:  -1,
				},
				kubeQOSDirParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  2,
					corev1.PodQOSBestEffort: -1,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 2,
					corev1.PodQOSBurstable:  2,
					corev1.PodQOSBestEffort: -1,
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "parse different cpu qos policy",
			args: args{
				mergedNodeSLO: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						Policies: &slov1alpha1.ResourceQOSPolicies{
							CPUPolicy: &policyCoreSched,
						},
						LSRClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						LSClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(2),
								},
							},
						},
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								Enable: pointer.Bool(true),
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(-1),
								},
							},
						},
					},
				},
			},
			wantRule: bvtRule{
				enable: false,
				podQOSParams: map[ext.QoSClass]int64{
					ext.QoSLSE: 0,
					ext.QoSLSR: 0,
					ext.QoSLS:  0,
					ext.QoSBE:  0,
				},
				kubeQOSDirParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  0,
					corev1.PodQOSBestEffort: 0,
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]int64{
					corev1.PodQOSGuaranteed: 0,
					corev1.PodQOSBurstable:  0,
					corev1.PodQOSBestEffort: 0,
				},
			},
			want:    true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &bvtPlugin{
				rule: tt.args.rule,
			}
			got, err := b.parseRule(tt.args.mergedNodeSLO)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRule() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseRule() got = %v, want %v", got, tt.want)
			}
			assert.Equal(t, tt.wantRule, *b.getRule(), "parse bvt rule not equal")
		})
	}
}

func Test_bvtPlugin_ruleUpdateCbForPods(t *testing.T) {
	type fields struct {
		rule *bvtRule
	}
	type args struct {
		pods map[string]*statesinformer.PodMeta
	}
	tests := []struct {
		name           string
		fields         fields
		args           args
		wantKubeDirVal map[corev1.PodQOSClass]int64
		wantPodVal     map[string]int64
		wantErr        bool
	}{
		{
			name: "callback with ls and be enabled",
			fields: fields{
				rule: &bvtRule{
					podQOSParams: map[ext.QoSClass]int64{
						ext.QoSLSR: 0,
						ext.QoSLS:  2,
						ext.QoSBE:  -1,
					},
					kubeQOSDirParams: map[corev1.PodQOSClass]int64{
						corev1.PodQOSGuaranteed: 0,
						corev1.PodQOSBurstable:  2,
						corev1.PodQOSBestEffort: -1,
					},
					kubeQOSPodParams: map[corev1.PodQOSClass]int64{
						corev1.PodQOSGuaranteed: 2,
						corev1.PodQOSBurstable:  2,
						corev1.PodQOSBestEffort: -1,
					},
				},
			},
			args: args{
				pods: map[string]*statesinformer.PodMeta{
					"lsr-pod": {
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "lsr-pod",
								Labels: map[string]string{
									ext.LabelPodQoS: string(ext.QoSLSR),
								},
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSGuaranteed,
							},
						},
						CgroupDir: "/kubepods-test-lsr-pod.slice",
					},
					"ls-pod1": {
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "ls-pod1",
								Labels: map[string]string{
									ext.LabelPodQoS: string(ext.QoSLS),
								},
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSGuaranteed,
							},
						},
						CgroupDir: "/kubepods-test-ls-pod1.slice",
					},
					"ls-pod2": {
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "ls-pod2",
								Labels: map[string]string{
									ext.LabelPodQoS: string(ext.QoSLS),
								},
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSBurstable,
							},
						},
						CgroupDir: "/kubepods-burstable.slice/kubepods-test-ls-pod2.slice",
					},
					"be-pod": {
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "be-pod",
								Labels: map[string]string{
									ext.LabelPodQoS: string(ext.QoSBE),
								},
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSBestEffort,
							},
						},
						CgroupDir: "/kubepods-besteffort.slice/kubepods-test-be-pod.slice",
					},
					"guaranteed-pod": {
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "guaranteed-pod",
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSGuaranteed,
							},
						},
						CgroupDir: "/kubepods-test-besteffort-pod.slice",
					},
					"burstable-pod": {
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "burstable-pod",
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSBurstable,
							},
						},
						CgroupDir: "/kubepods-burstable.slice/kubepods-test-burstable-pod.slice",
					},
					"besteffort-pod": {
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "besteffort-pod",
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSBestEffort,
							},
						},
						CgroupDir: "/kubepods-besteffort.slice/kubepods-test-besteffort-pod.slice",
					},
				},
			},
			wantKubeDirVal: map[corev1.PodQOSClass]int64{
				corev1.PodQOSGuaranteed: int64(0),
				corev1.PodQOSBurstable:  int64(2),
				corev1.PodQOSBestEffort: int64(-1),
			},
			wantPodVal: map[string]int64{
				"lsr-pod":        0,
				"ls-pod1":        2,
				"ls-pod2":        2,
				"be-pod":         -1,
				"guaranteed-pod": 2,
				"burstable-pod":  2,
				"besteffort-pod": -1,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		testHelper := system.NewFileTestUtil(t)
		for _, kubeQoS := range []corev1.PodQOSClass{corev1.PodQOSGuaranteed, corev1.PodQOSBurstable, corev1.PodQOSBestEffort} {
			initCPUBvt(util.GetPodQoSRelativePath(kubeQoS), 0, testHelper)
		}
		podList := make([]*statesinformer.PodMeta, 0, len(tt.args.pods))
		for _, pod := range tt.args.pods {
			initCPUBvt(pod.CgroupDir, 0, testHelper)
			podList = append(podList, pod)
		}
		target := &statesinformer.CallbackTarget{
			Pods: podList,
		}
		t.Run(tt.name, func(t *testing.T) {
			b := &bvtPlugin{
				rule:     tt.fields.rule,
				executor: resourceexecutor.NewResourceUpdateExecutor(),
			}
			stop := make(chan struct{})
			defer func() { close(stop) }()
			b.executor.Run(stop)

			if err := b.ruleUpdateCb(target); (err != nil) != tt.wantErr {
				t.Errorf("ruleUpdateCb() error = %v, wantErr %v", err, tt.wantErr)
			}
			for kubeQoS, wantBvt := range tt.wantKubeDirVal {
				gotBvtStr := testHelper.ReadCgroupFileContents(util.GetPodQoSRelativePath(kubeQoS), system.CPUBVTWarpNs)
				gotBvt, _ := strconv.ParseInt(gotBvtStr, 10, 64)
				assert.Equal(t, wantBvt, gotBvt, "qos %s bvt value not equal", kubeQoS)
			}
			for podName, pod := range tt.args.pods {
				gotBvtStr := testHelper.ReadCgroupFileContents(pod.CgroupDir, system.CPUBVTWarpNs)
				gotBvt, _ := strconv.ParseInt(gotBvtStr, 10, 64)
				wantBvt := tt.wantPodVal[podName]
				assert.Equal(t, wantBvt, gotBvt, "pod %s bvt value not equal", podName)
			}
		})
	}
}

func Test_bvtPlugin_ruleUpdateCbForHostApp(t *testing.T) {
	type fields struct {
		rule *bvtRule
	}
	type args struct {
		hostApp slov1alpha1.HostApplicationSpec
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantBvt int64
		wantErr bool
	}{
		{
			name: "set host application bvt",
			fields: fields{
				rule: &bvtRule{
					podQOSParams: map[ext.QoSClass]int64{
						ext.QoSLSR: 0,
						ext.QoSLS:  2,
						ext.QoSBE:  -1,
					},
					kubeQOSDirParams: map[corev1.PodQOSClass]int64{
						corev1.PodQOSGuaranteed: 0,
						corev1.PodQOSBurstable:  2,
						corev1.PodQOSBestEffort: -1,
					},
					kubeQOSPodParams: map[corev1.PodQOSClass]int64{
						corev1.PodQOSGuaranteed: 2,
						corev1.PodQOSBurstable:  2,
						corev1.PodQOSBestEffort: -1,
					},
				},
			},
			args: args{
				hostApp: slov1alpha1.HostApplicationSpec{
					Name: "test-app",
					QoS:  ext.QoSLS,
					CgroupPath: &slov1alpha1.CgroupPath{
						ParentDir:    "test-ls",
						RelativePath: "test-app",
					},
				},
			},
			wantBvt: 2,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHelper := system.NewFileTestUtil(t)
			testApp := tt.args.hostApp
			if testApp.CgroupPath == nil ||
				(testApp.CgroupPath.Base != "" && testApp.CgroupPath.Base != slov1alpha1.CgroupBaseTypeRoot) {
				t.Errorf("only cgroup root dir is suupported")
			}

			cgroupDir := filepath.Join(testApp.CgroupPath.ParentDir, testApp.CgroupPath.RelativePath)
			initCPUBvt(cgroupDir, 0, testHelper)
			b := &bvtPlugin{
				rule:          tt.fields.rule,
				sysSupported:  pointer.Bool(true),
				kernelEnabled: pointer.Bool(true),
				executor:      resourceexecutor.NewResourceUpdateExecutor(),
			}
			stop := make(chan struct{})
			defer func() { close(stop) }()
			b.executor.Run(stop)
			target := &statesinformer.CallbackTarget{
				HostApplications: []slov1alpha1.HostApplicationSpec{tt.args.hostApp},
			}

			if err := b.ruleUpdateCb(target); (err != nil) != tt.wantErr {
				t.Errorf("ruleUpdateCb() error = %v, wantErr %v", err, tt.wantErr)
			}

			gotBvtStr := testHelper.ReadCgroupFileContents(cgroupDir, system.CPUBVTWarpNs)
			gotBvt, _ := strconv.ParseInt(gotBvtStr, 10, 64)
			assert.Equal(t, tt.wantBvt, gotBvt)
		})
	}
}
