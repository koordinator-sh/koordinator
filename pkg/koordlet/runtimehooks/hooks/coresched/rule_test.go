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

package coresched

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
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func TestRule(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		r := newRule()
		assert.NotNil(t, r)
		assert.False(t, r.IsInited())

		ruleNew := testGetDisabledRule()
		got := r.Update(ruleNew)
		assert.True(t, true, got)
		assert.True(t, r.IsInited())
		got, got1 := r.IsPodEnabled(extension.QoSLS, corev1.PodQOSGuaranteed)
		assert.False(t, got)
		assert.False(t, got1)
		got2 := r.IsKubeQOSCPUIdle(corev1.PodQOSBurstable)
		assert.False(t, got2)
		got, got1 = r.IsPodEnabled(extension.QoSNone, corev1.PodQOSBestEffort)
		assert.False(t, got)
		assert.False(t, got1)

		got = r.Update(ruleNew)
		assert.False(t, false, got)
		got, got1 = r.IsPodEnabled(extension.QoSBE, corev1.PodQOSBestEffort)
		assert.False(t, got)
		assert.False(t, got1)
		got2 = r.IsKubeQOSCPUIdle(corev1.PodQOSBestEffort)
		assert.False(t, got2)

		ruleNew = testGetEnabledRule()
		got = r.Update(ruleNew)
		assert.True(t, true, got)
		got, got1 = r.IsPodEnabled(extension.QoSLS, corev1.PodQOSGuaranteed)
		assert.True(t, got)
		assert.True(t, got1)
		got, got1 = r.IsPodEnabled(extension.QoSNone, corev1.PodQOSBurstable)
		assert.True(t, got)
		assert.True(t, got1)
		got, got1 = r.IsPodEnabled(extension.QoSBE, corev1.PodQOSBestEffort)
		assert.True(t, got)
		assert.False(t, got1)
		got2 = r.IsKubeQOSCPUIdle(corev1.PodQOSBurstable)
		assert.False(t, got2)

		// enable CPU idle for BE
		beParam := Param{
			IsPodEnabled: true,
			IsExpeller:   false,
			IsCPUIdle:    true,
		}
		ruleNew.podQOSParams[extension.QoSBE] = beParam
		ruleNew.kubeQOSPodParams[corev1.PodQOSBestEffort] = beParam
		got2 = r.IsKubeQOSCPUIdle(corev1.PodQOSBestEffort)
		assert.True(t, got2)
	})
}

func Test_parseRuleForNodeSLO(t *testing.T) {
	type field struct {
		rule *Rule
	}
	tests := []struct {
		name      string
		field     field
		arg       interface{}
		want      bool
		wantErr   bool
		wantField *Rule
	}{
		{
			name: "keep disabled",
			field: field{
				rule: testGetDisabledRule(),
			},
			arg: &slov1alpha1.NodeSLOSpec{
				ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
					Policies: sloconfig.NoneResourceQOSPolicies(),
					LSRClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(false),
							CPUQOS: *sloconfig.NoneCPUQOS(),
						},
					},
					LSClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(false),
							CPUQOS: *sloconfig.NoneCPUQOS(),
						},
					},
					BEClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(false),
							CPUQOS: *sloconfig.NoneCPUQOS(),
						},
					},
				},
			},
			want:      false,
			wantErr:   false,
			wantField: testGetDisabledRule(),
		},
		{
			name: "keep enabled",
			field: field{
				rule: testGetEnabledRule(),
			},
			arg: &slov1alpha1.NodeSLOSpec{
				ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
					Policies: testGetEnabledResourceQOSPolicies(),
					LSRClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: *sloconfig.DefaultCPUQOS(extension.QoSLSR),
						},
					},
					LSClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: *sloconfig.DefaultCPUQOS(extension.QoSLS),
						},
					},
					BEClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: *sloconfig.DefaultCPUQOS(extension.QoSBE),
						},
					},
				},
			},
			want:      false,
			wantErr:   false,
			wantField: testGetEnabledRule(),
		},
		{
			name: "policy disabled",
			field: field{
				rule: testGetEnabledRule(),
			},
			arg: &slov1alpha1.NodeSLOSpec{
				ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
					Policies: sloconfig.NoneResourceQOSPolicies(),
					LSRClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(false),
							CPUQOS: *sloconfig.NoneCPUQOS(),
						},
					},
					LSClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(false),
							CPUQOS: *sloconfig.NoneCPUQOS(),
						},
					},
					BEClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(false),
							CPUQOS: *sloconfig.NoneCPUQOS(),
						},
					},
				},
			},
			want:      true,
			wantErr:   false,
			wantField: testGetDisabledRule(),
		},
		{
			name: "policy enabled",
			field: field{
				rule: testGetDisabledRule(),
			},
			arg: &slov1alpha1.NodeSLOSpec{
				ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
					Policies: testGetEnabledResourceQOSPolicies(),
					LSRClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: *sloconfig.DefaultCPUQOS(extension.QoSLSR),
						},
					},
					LSClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: *sloconfig.DefaultCPUQOS(extension.QoSLS),
						},
					},
					BEClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: *sloconfig.DefaultCPUQOS(extension.QoSBE),
						},
					},
				},
			},
			want:      true,
			wantErr:   false,
			wantField: testGetEnabledRule(),
		},
		{
			name: "enabled on LS and BE",
			field: field{
				rule: testGetDisabledRule(),
			},
			arg: &slov1alpha1.NodeSLOSpec{
				ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
					Policies: testGetEnabledResourceQOSPolicies(),
					LSRClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(false),
							CPUQOS: *sloconfig.NoneCPUQOS(),
						},
					},
					LSClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: slov1alpha1.CPUQOS{
								GroupIdentity: pointer.Int64(2),
								SchedIdle:     pointer.Int64(0),
								CoreExpeller:  pointer.Bool(true),
							},
						},
					},
					BEClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: slov1alpha1.CPUQOS{
								GroupIdentity: pointer.Int64(-1),
								SchedIdle:     pointer.Int64(1),
								CoreExpeller:  pointer.Bool(false),
							},
						},
					},
				},
			},
			want:    true,
			wantErr: false,
			wantField: &Rule{
				podQOSParams: map[extension.QoSClass]Param{
					extension.QoSLSE: {
						IsPodEnabled: false,
						IsExpeller:   false,
						IsCPUIdle:    false,
					},
					extension.QoSLSR: {
						IsPodEnabled: false,
						IsExpeller:   false,
						IsCPUIdle:    false,
					},
					extension.QoSLS: {
						IsPodEnabled: true,
						IsExpeller:   true,
						IsCPUIdle:    false,
					},
					extension.QoSBE: {
						IsPodEnabled: true,
						IsExpeller:   false,
						IsCPUIdle:    true,
					},
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]Param{
					corev1.PodQOSGuaranteed: {
						IsPodEnabled: true,
						IsExpeller:   true,
						IsCPUIdle:    false,
					},
					corev1.PodQOSBurstable: {
						IsPodEnabled: true,
						IsExpeller:   true,
						IsCPUIdle:    false,
					},
					corev1.PodQOSBestEffort: {
						IsPodEnabled: true,
						IsExpeller:   false,
						IsCPUIdle:    true,
					},
				},
			},
		},
		{
			name: "policy enabled on BE",
			field: field{
				rule: &Rule{
					podQOSParams: map[extension.QoSClass]Param{
						extension.QoSLSE: {
							IsPodEnabled: true,
							IsExpeller:   true,
							IsCPUIdle:    false,
						},
						extension.QoSLSR: {
							IsPodEnabled: true,
							IsExpeller:   true,
							IsCPUIdle:    false,
						},
						extension.QoSLS: {
							IsPodEnabled: true,
							IsExpeller:   true,
							IsCPUIdle:    false,
						},
						extension.QoSBE: {
							IsPodEnabled: false,
							IsExpeller:   false,
							IsCPUIdle:    false,
						},
					},
					kubeQOSPodParams: map[corev1.PodQOSClass]Param{
						corev1.PodQOSGuaranteed: {
							IsPodEnabled: true,
							IsExpeller:   true,
							IsCPUIdle:    false,
						},
						corev1.PodQOSBurstable: {
							IsPodEnabled: true,
							IsExpeller:   true,
							IsCPUIdle:    false,
						},
						corev1.PodQOSBestEffort: {
							IsPodEnabled: false,
							IsExpeller:   false,
							IsCPUIdle:    false,
						},
					},
				},
			},
			arg: &slov1alpha1.NodeSLOSpec{
				ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
					Policies: testGetEnabledResourceQOSPolicies(),
					LSRClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: *sloconfig.DefaultCPUQOS(extension.QoSLSR),
						},
					},
					LSClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: *sloconfig.DefaultCPUQOS(extension.QoSLS),
						},
					},
					BEClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							Enable: pointer.Bool(true),
							CPUQOS: slov1alpha1.CPUQOS{
								GroupIdentity: pointer.Int64(-1),
								SchedIdle:     pointer.Int64(1),
								CoreExpeller:  pointer.Bool(false),
							},
						},
					},
				},
			},
			want:    true,
			wantErr: false,
			wantField: &Rule{
				podQOSParams: map[extension.QoSClass]Param{
					extension.QoSLSE: {
						IsPodEnabled: true,
						IsExpeller:   true,
						IsCPUIdle:    false,
					},
					extension.QoSLSR: {
						IsPodEnabled: true,
						IsExpeller:   true,
						IsCPUIdle:    false,
					},
					extension.QoSLS: {
						IsPodEnabled: true,
						IsExpeller:   true,
						IsCPUIdle:    false,
					},
					extension.QoSBE: {
						IsPodEnabled: true,
						IsExpeller:   false,
						IsCPUIdle:    true,
					},
				},
				kubeQOSPodParams: map[corev1.PodQOSClass]Param{
					corev1.PodQOSGuaranteed: {
						IsPodEnabled: true,
						IsExpeller:   true,
						IsCPUIdle:    false,
					},
					corev1.PodQOSBurstable: {
						IsPodEnabled: true,
						IsExpeller:   true,
						IsCPUIdle:    false,
					},
					corev1.PodQOSBestEffort: {
						IsPodEnabled: true,
						IsExpeller:   false,
						IsCPUIdle:    true,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				rule: tt.field.rule,
			}
			got, gotErr := p.parseRuleForNodeSLO(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantField, p.rule)
		})
	}
}

func Test_parseForAllPods(t *testing.T) {
	type fields struct {
		preparePluginFn func(p *Plugin)
	}
	tests := []struct {
		name    string
		fields  fields
		arg     interface{}
		want    bool
		wantErr bool
	}{
		{
			name:    "parse rule failed",
			arg:     nil,
			want:    false,
			wantErr: true,
		},
		{
			name:    "trigger callback since not synced",
			arg:     &struct{}{},
			want:    true,
			wantErr: false,
		},
		{
			name: "not trigger callback since synced",
			fields: fields{
				preparePluginFn: func(p *Plugin) {
					p.allPodsSyncOnce.Do(func() {})
				},
			},
			arg:     &struct{}{},
			want:    false,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			if tt.fields.preparePluginFn != nil {
				tt.fields.preparePluginFn(p)
			}
			got, gotErr := p.parseForAllPods(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}

func Test_ruleUpdateCb(t *testing.T) {
	type fields struct {
		prepareFn       func(helper *sysutil.FileTestUtil)
		plugin          *Plugin
		preparePluginFn func(p *Plugin)
		cse             sysutil.CoreSchedExtendedInterface
	}
	type wantFields struct {
		rule               *Rule
		sysSupported       *bool
		initialized        bool
		cookieToPGIDs      map[uint64][]uint32
		groupToCookie      map[string]uint64
		parentDirToCPUIdle map[string]int64
	}
	tests := []struct {
		name       string
		fields     fields
		arg        *statesinformer.CallbackTarget
		wantErr    bool
		wantFields wantFields
	}{
		{
			name: "target invalid",
			fields: fields{
				plugin: newPlugin(),
			},
			arg:     nil,
			wantErr: true,
			wantFields: wantFields{
				rule:         newRule(),
				sysSupported: nil,
				initialized:  false,
			},
		},
		{
			name: "rule not inited",
			fields: fields{
				plugin: newPlugin(),
			},
			arg: &statesinformer.CallbackTarget{
				Pods: []*statesinformer.PodMeta{},
			},
			wantErr: false,
			wantFields: wantFields{
				rule:         newRule(),
				sysSupported: nil,
				initialized:  false,
			},
		},
		{
			name: "system does not support core sched",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					featuresPath := sysutil.SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C`)
				},
				plugin: newPlugin(),
				preparePluginFn: func(p *Plugin) {
					p.rule = testGetEnabledRule()
				},
			},
			arg: &statesinformer.CallbackTarget{
				Pods: []*statesinformer.PodMeta{},
			},
			wantErr: false,
			wantFields: wantFields{
				rule:         testGetEnabledRule(),
				sysSupported: pointer.Bool(false),
				initialized:  false,
			},
		},
		{
			name: "no cookie has been synced",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					featuresPath := sysutil.SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C CORE_SCHED`)
					cpuIdle, err := sysutil.GetCgroupResource(sysutil.CPUIdleName)
					assert.NoError(t, err)
					guaranteedQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					burstableQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBurstable)
					besteffortQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(guaranteedQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(burstableQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(besteffortQOSDir, cpuIdle, "0")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					p.executor = resourceexecutor.NewTestResourceExecutor()
					p.initialized.Store(false)
				},
			},
			arg: &statesinformer.CallbackTarget{
				Pods: []*statesinformer.PodMeta{},
			},
			wantErr: false,
			wantFields: wantFields{
				rule:         testGetEnabledRule(),
				sysSupported: pointer.Bool(true),
				initialized:  false,
				parentDirToCPUIdle: map[string]int64{
					util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed): 0,
					util.GetPodQoSRelativePath(corev1.PodQOSBurstable):  0,
					util.GetPodQoSRelativePath(corev1.PodQOSBestEffort): 0,
				},
			},
		},
		{
			name: "sync cookie correctly",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					featuresPath := sysutil.SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C CORE_SCHED`)
					cpuset, err := sysutil.GetCgroupResource(sysutil.CPUSetCPUSName)
					assert.NoError(t, err)
					cpuIdle, err := sysutil.GetCgroupResource(sysutil.CPUIdleName)
					assert.NoError(t, err)
					guaranteedQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					burstableQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBurstable)
					besteffortQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(guaranteedQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(burstableQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(besteffortQOSDir, cpuIdle, "0")
					sandboxContainerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice", cpuIdle, "0")
					containerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice", cpuIdle, "0")
					statPath0 := sysutil.GetProcPIDStatPath(12340)
					helper.WriteFileContents(statPath0, `12340 (stress) S 12340 12340 12340 12300 12340 123400 100 0 0 0 0 0 ...`)
					statPath := sysutil.GetProcPIDStatPath(12344)
					helper.WriteFileContents(statPath, `12344 (stress) S 12340 12344 12340 12300 12344 123450 151 0 0 0 0 0 ...`)
					statPath1 := sysutil.GetProcPIDStatPath(12345)
					helper.WriteFileContents(statPath1, `12345 (stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`)
					statPath2 := sysutil.GetProcPIDStatPath(12346)
					helper.WriteFileContents(statPath2, `12346 (stress) S 12340 12346 12340 12300 12346 123450 151 0 0 0 0 0 ...`)
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					p.executor = resourceexecutor.NewTestResourceExecutor()
					p.initialized.Store(false)
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
			},
			arg: &statesinformer.CallbackTarget{
				Pods: []*statesinformer.PodMeta{
					{
						CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod",
								UID:  "xxxxxx",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container",
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
								Phase:    corev1.PodRunning,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://yyyyyy",
										State: corev1.ContainerState{
											Running: &corev1.ContainerStateRunning{},
										},
									},
								},
							},
						},
					},
					{
						CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod-1",
								UID:  "nnnnnn",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-nnn",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLSR),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container-1",
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
								Phase:    corev1.PodFailed,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://mmmmmm",
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantFields: wantFields{
				rule:         testGetEnabledRule(),
				sysSupported: pointer.Bool(true),
				initialized:  true,
				cookieToPGIDs: map[uint64][]uint32{
					1000000: {
						12340,
						12344,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
				parentDirToCPUIdle: map[string]int64{
					util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed): 0,
					util.GetPodQoSRelativePath(corev1.PodQOSBurstable):  0,
					util.GetPodQoSRelativePath(corev1.PodQOSBestEffort): 0,
					"kubepods.slice/kubepods-podxxxxxx.slice":           0,
				},
			},
		},
		{
			name: "sync cookie correctly with CPU idle enabled",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					featuresPath := sysutil.SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C CORE_SCHED`)
					cpuset, err := sysutil.GetCgroupResource(sysutil.CPUSetCPUSName)
					assert.NoError(t, err)
					cpuIdle, err := sysutil.GetCgroupResource(sysutil.CPUIdleName)
					assert.NoError(t, err)
					guaranteedQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					burstableQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBurstable)
					besteffortQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(guaranteedQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(burstableQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(besteffortQOSDir, cpuIdle, "0")
					sandboxContainerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice", cpuIdle, "0")
					containerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice", cpuIdle, "0")
					statPath0 := sysutil.GetProcPIDStatPath(12340)
					helper.WriteFileContents(statPath0, `12340 (stress) S 12340 12340 12340 12300 12340 123400 100 0 0 0 0 0 ...`)
					statPath := sysutil.GetProcPIDStatPath(12344)
					helper.WriteFileContents(statPath, `12344 (stress) S 12340 12344 12340 12300 12344 123450 151 0 0 0 0 0 ...`)
					statPath1 := sysutil.GetProcPIDStatPath(12345)
					helper.WriteFileContents(statPath1, `12345 (stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`)
					statPath2 := sysutil.GetProcPIDStatPath(12346)
					helper.WriteFileContents(statPath2, `12346 (stress) S 12340 12346 12340 12300 12346 123450 151 0 0 0 0 0 ...`)
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					p.rule = testGetAllEnabledRule()
					p.executor = resourceexecutor.NewTestResourceExecutor()
					p.initialized.Store(false)
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
			},
			arg: &statesinformer.CallbackTarget{
				Pods: []*statesinformer.PodMeta{
					{
						CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod",
								UID:  "xxxxxx",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container",
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
								Phase:    corev1.PodRunning,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://yyyyyy",
										State: corev1.ContainerState{
											Running: &corev1.ContainerStateRunning{},
										},
									},
								},
							},
						},
					},
					{
						CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod-1",
								UID:  "nnnnnn",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-nnn",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLSR),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container-1",
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
								Phase:    corev1.PodFailed,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://mmmmmm",
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantFields: wantFields{
				rule:         testGetAllEnabledRule(),
				sysSupported: pointer.Bool(true),
				initialized:  true,
				cookieToPGIDs: map[uint64][]uint32{
					1000000: {
						12340,
						12344,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
				parentDirToCPUIdle: map[string]int64{
					util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed): 0,
					util.GetPodQoSRelativePath(corev1.PodQOSBurstable):  0,
					util.GetPodQoSRelativePath(corev1.PodQOSBestEffort): 1,
					"kubepods.slice/kubepods-podxxxxxx.slice":           0,
				},
			},
		},
		{
			name: "sync cookie correctly excluding SYSTEM pods",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					featuresPath := sysutil.SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C CORE_SCHED`)
					cpuset, err := sysutil.GetCgroupResource(sysutil.CPUSetCPUSName)
					assert.NoError(t, err)
					cpuIdle, err := sysutil.GetCgroupResource(sysutil.CPUIdleName)
					assert.NoError(t, err)
					guaranteedQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					burstableQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBurstable)
					besteffortQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(guaranteedQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(burstableQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(besteffortQOSDir, cpuIdle, "0")
					sandboxContainerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice", cpuIdle, "0")
					containerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice", cpuIdle, "0")
					statPath0 := sysutil.GetProcPIDStatPath(12340)
					helper.WriteFileContents(statPath0, `12340 (stress) S 12340 12340 12340 12300 12340 123400 100 0 0 0 0 0 ...`)
					statPath := sysutil.GetProcPIDStatPath(12344)
					helper.WriteFileContents(statPath, `12344 (stress) S 12340 12344 12340 12300 12344 123450 151 0 0 0 0 0 ...`)
					statPath1 := sysutil.GetProcPIDStatPath(12345)
					helper.WriteFileContents(statPath1, `12345 (stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`)
					statPath2 := sysutil.GetProcPIDStatPath(12346)
					helper.WriteFileContents(statPath2, `12346 (stress) S 12340 12346 12340 12300 12346 123450 151 0 0 0 0 0 ...`)
					sandboxContainerCgroupDir1 := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-podssssss.slice", "containerd://eeeeee")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, sysutil.CPUProcs, "100\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, sysutil.CPUProcsV2, "100\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-burstable.slice/kubepods-burstable-podssssss.slice", cpuIdle, "0")
					statPath3 := sysutil.GetProcPIDStatPath(100)
					helper.WriteFileContents(statPath3, `100 (koordlet) S 100 100 100 100 100 2000 50 0 0 0 0 0 ...`)
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					p.executor = resourceexecutor.NewTestResourceExecutor()
					p.initialized.Store(false)
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
			},
			arg: &statesinformer.CallbackTarget{
				Pods: []*statesinformer.PodMeta{
					{
						CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod",
								UID:  "xxxxxx",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container",
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
								Phase:    corev1.PodRunning,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://yyyyyy",
										State: corev1.ContainerState{
											Running: &corev1.ContainerStateRunning{},
										},
									},
								},
							},
						},
					},
					{
						CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod-1",
								UID:  "nnnnnn",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-nnn",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLSR),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container-1",
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
								Phase:    corev1.PodFailed,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://mmmmmm",
									},
								},
							},
						},
					},
					{
						CgroupDir: "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-podssssss.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod-s",
								UID:  "ssssss",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-sss",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSSystem),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container-s",
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("0"),
												corev1.ResourceMemory: resource.MustParse("0"),
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
								Phase:    corev1.PodRunning,
								QOSClass: corev1.PodQOSBurstable,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container-s",
										ContainerID: "containerd://tttttt",
										State: corev1.ContainerState{
											Running: &corev1.ContainerStateRunning{},
										},
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantFields: wantFields{
				rule:         testGetEnabledRule(),
				sysSupported: pointer.Bool(true),
				initialized:  true,
				cookieToPGIDs: map[uint64][]uint32{
					1000000: {
						12340,
						12344,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
				parentDirToCPUIdle: map[string]int64{
					util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed):                          0,
					util.GetPodQoSRelativePath(corev1.PodQOSBurstable):                           0,
					util.GetPodQoSRelativePath(corev1.PodQOSBestEffort):                          0,
					"kubepods.slice/kubepods-podxxxxxx.slice":                                    0,
					"kubepods.slice/kubepods-burstable.slice/kubepods-burstable-podssssss.slice": 0,
				},
			},
		},
		{
			name: "sync cookie correctly with expeller and non-expeller groups",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					featuresPath := sysutil.SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C CORE_SCHED`)
					cpuset, err := sysutil.GetCgroupResource(sysutil.CPUSetCPUSName)
					assert.NoError(t, err)
					cpuIdle, err := sysutil.GetCgroupResource(sysutil.CPUIdleName)
					assert.NoError(t, err)
					guaranteedQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					burstableQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBurstable)
					besteffortQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(guaranteedQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(burstableQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(besteffortQOSDir, cpuIdle, "0")
					sandboxContainerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice", cpuIdle, "0")
					containerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice", cpuIdle, "0")
					statPath0 := sysutil.GetProcPIDStatPath(12340)
					helper.WriteFileContents(statPath0, `12340 (stress) S 12340 12340 12340 12300 12340 123400 100 0 0 0 0 0 ...`)
					statPath := sysutil.GetProcPIDStatPath(12344)
					helper.WriteFileContents(statPath, `12344 (stress) S 12340 12344 12340 12300 12344 123450 151 0 0 0 0 0 ...`)
					statPath1 := sysutil.GetProcPIDStatPath(12345)
					helper.WriteFileContents(statPath1, `12345 (stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`)
					statPath2 := sysutil.GetProcPIDStatPath(12346)
					helper.WriteFileContents(statPath2, `12346 (stress) S 12340 12346 12340 12300 12346 123450 151 0 0 0 0 0 ...`)
					sandboxContainerCgroupDir1 := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podssssss.slice", "containerd://eeeeee")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, sysutil.CPUProcs, "100\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, sysutil.CPUProcsV2, "100\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podssssss.slice", cpuIdle, "0")
					statPath3 := sysutil.GetProcPIDStatPath(100)
					helper.WriteFileContents(statPath3, `100 (koordlet) S 100 100 100 100 100 2000 50 0 0 0 0 0 ...`)
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					p.executor = resourceexecutor.NewTestResourceExecutor()
					p.initialized.Store(false)
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					100:   2000,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					100:   100,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
			},
			arg: &statesinformer.CallbackTarget{
				Pods: []*statesinformer.PodMeta{
					{
						CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod",
								UID:  "xxxxxx",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container",
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
								Phase:    corev1.PodRunning,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://yyyyyy",
										State: corev1.ContainerState{
											Running: &corev1.ContainerStateRunning{},
										},
									},
								},
							},
						},
					},
					{
						CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod-1",
								UID:  "nnnnnn",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-nnn",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLSR),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container-1",
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
								Phase:    corev1.PodFailed,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://mmmmmm",
									},
								},
							},
						},
					},
					{
						CgroupDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podssssss.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod-s",
								UID:  "ssssss",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-sss",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container-s",
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												extension.BatchCPU:    resource.MustParse("0"),
												extension.BatchMemory: resource.MustParse("0"),
											},
											Limits: corev1.ResourceList{
												extension.BatchCPU:    resource.MustParse("2000"),
												extension.BatchMemory: resource.MustParse("4Gi"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase:    corev1.PodRunning,
								QOSClass: corev1.PodQOSBestEffort,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container-s",
										ContainerID: "containerd://tttttt",
										State: corev1.ContainerState{
											Running: &corev1.ContainerStateRunning{},
										},
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantFields: wantFields{
				rule:         testGetEnabledRule(),
				sysSupported: pointer.Bool(true),
				initialized:  true,
				cookieToPGIDs: map[uint64][]uint32{
					2000: {
						100,
					},
					1000000: {
						12340,
						12344,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
					"group-sss":          2000,
				},
				parentDirToCPUIdle: map[string]int64{
					util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed):                            0,
					util.GetPodQoSRelativePath(corev1.PodQOSBurstable):                             0,
					util.GetPodQoSRelativePath(corev1.PodQOSBestEffort):                            0,
					"kubepods.slice/kubepods-podxxxxxx.slice":                                      0,
					"kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podssssss.slice": 0,
				},
			},
		},
		{
			name: "sync cookie correctly for multiple containers",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					featuresPath := sysutil.SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C CORE_SCHED`)
					cpuset, err := sysutil.GetCgroupResource(sysutil.CPUSetCPUSName)
					assert.NoError(t, err)
					cpuIdle, err := sysutil.GetCgroupResource(sysutil.CPUIdleName)
					assert.NoError(t, err)
					guaranteedQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					burstableQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBurstable)
					besteffortQOSDir := util.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(guaranteedQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(burstableQOSDir, cpuIdle, "0")
					helper.WriteCgroupFileContents(besteffortQOSDir, cpuIdle, "0")
					sandboxContainerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, cpuset, "0-127")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice", cpuIdle, "0")
					containerCgroupDir := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, cpuset, "0-127")
					statPath0 := sysutil.GetProcPIDStatPath(12340)
					helper.WriteFileContents(statPath0, `12340 (stress) S 12340 12340 12340 12300 12340 123400 100 0 0 0 0 0 ...`)
					statPath := sysutil.GetProcPIDStatPath(12344)
					helper.WriteFileContents(statPath, `12344 (stress) S 12340 12344 12340 12300 12344 123450 151 0 0 0 0 0 ...`)
					statPath1 := sysutil.GetProcPIDStatPath(12345)
					helper.WriteFileContents(statPath1, `12345 (stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`)
					statPath2 := sysutil.GetProcPIDStatPath(12346)
					helper.WriteFileContents(statPath2, `12346 (stress) S 12340 12346 12340 12300 12346 123450 151 0 0 0 0 0 ...`)
					containerCgroupDir1 := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podxxxxxx.slice", "containerd://zzzzzz")
					helper.WriteCgroupFileContents(containerCgroupDir1, sysutil.CPUProcs, "12350\n")
					helper.WriteCgroupFileContents(containerCgroupDir1, sysutil.CPUProcsV2, "12350\n")
					helper.WriteCgroupFileContents(containerCgroupDir1, cpuset, "0-127")
					statPath3 := sysutil.GetProcPIDStatPath(12350)
					helper.WriteFileContents(statPath3, `12350 (stress) S 12350 12350 12350 12350 12350 123500 200 0 0 0 0 0 ...`)
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podnnnnnn.slice", cpuIdle, "0")
					sandboxContainerCgroupDir1 := testGetContainerCgroupParentDir(t, "kubepods.slice/kubepods-podnnnnnn.slice", "containerd://mmmmmm")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, sysutil.CPUProcs, "15000\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, sysutil.CPUProcsV2, "15000\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, cpuset, "9-12,73-76")
					statPath4 := sysutil.GetProcPIDStatPath(15000)
					helper.WriteFileContents(statPath4, `15000 (bash) S 15000 15000 15000 15000 15000 150000 500 0 0 0 0 0 ...`)
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					p.executor = resourceexecutor.NewTestResourceExecutor()
					p.initialized.Store(false)
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
					p.cookieCache.SetDefault("group-xxx-expeller", newCookieCacheEntry(1000000, 12344, 12345, 12346))
					p.groupCache.SetDefault("xxxxxx/containerd://yyyyyy", "group-xxx-expeller")
					// test-pod-1 missing cookie cache
					p.groupCache.SetDefault("nnnnnn/containerd://mmmmmm", "group-yyy-expeller")
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					5000:  1000000,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
					12350: 1100000,
					15000: 0,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					5000:  5000,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
					12350: 12350,
					15000: 15000,
				}, map[uint32]bool{
					12346: true,
				}),
			},
			arg: &statesinformer.CallbackTarget{
				Pods: []*statesinformer.PodMeta{
					{
						CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod",
								UID:  "xxxxxx",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container",
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
									{
										Name: "test-container-1",
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
								Phase:    corev1.PodRunning,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://yyyyyy",
										State: corev1.ContainerState{
											Running: &corev1.ContainerStateRunning{},
										},
									},
									{
										Name:        "test-container-1",
										ContainerID: "containerd://zzzzzz",
										State: corev1.ContainerState{
											Running: &corev1.ContainerStateRunning{},
										},
									},
								},
							},
						},
					},
					{
						CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod-1",
								UID:  "nnnnnn",
								Annotations: map[string]string{
									slov1alpha1.AnnotationCoreSchedGroupID: "0",
								},
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLSR),
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: "test-container",
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
								Phase:    corev1.PodRunning,
								QOSClass: corev1.PodQOSGuaranteed,
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-container",
										ContainerID: "containerd://mmmmmm",
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
			wantFields: wantFields{
				rule:         testGetEnabledRule(),
				sysSupported: pointer.Bool(true),
				initialized:  true,
				cookieToPGIDs: map[uint64][]uint32{
					1000000: {
						12340,
						12344,
						12345,
						12346,
						12350,
					},
					2000000: {},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
				parentDirToCPUIdle: map[string]int64{
					util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed): 0,
					util.GetPodQoSRelativePath(corev1.PodQOSBurstable):  0,
					util.GetPodQoSRelativePath(corev1.PodQOSBestEffort): 0,
					"kubepods.slice/kubepods-podxxxxxx.slice":           0,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}
			p := tt.fields.plugin
			if tt.fields.cse != nil {
				p.cse = tt.fields.cse
			}
			if tt.fields.preparePluginFn != nil {
				tt.fields.preparePluginFn(p)
			}
			if p.executor != nil {
				stopCh := make(chan struct{})
				defer close(stopCh)
				p.executor.Run(stopCh)
			}

			gotErr := p.ruleUpdateCb(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.wantFields.rule, p.rule)
			assert.Equal(t, tt.wantFields.sysSupported, p.sysSupported)
			assert.Equal(t, tt.wantFields.initialized, p.initialized.Load())
			for groupID, cookieID := range tt.wantFields.groupToCookie {
				if cookieID <= 0 {
					_, ok := p.cookieCache.Get(groupID)
					assert.False(t, ok, groupID)
					continue
				}

				entryIf, ok := p.cookieCache.Get(groupID)
				assert.True(t, ok)
				entry, ok := entryIf.(*CookieCacheEntry)
				assert.True(t, ok)
				assert.Equal(t, cookieID, entry.CookieID, groupID)
				assert.Equal(t, len(tt.wantFields.cookieToPGIDs[cookieID]), len(entry.GetAllPGIDs()),
					"expect [%v] but got [%v]", tt.wantFields.cookieToPGIDs[cookieID], entry.GetAllPGIDs())
				for _, pgid := range tt.wantFields.cookieToPGIDs[cookieID] {
					assert.True(t, entry.HasPGID(pgid), pgid)
				}
			}
			for parentDir, wantValue := range tt.wantFields.parentDirToCPUIdle {
				cpuIdle, err := sysutil.GetCgroupResource(sysutil.CPUIdleName)
				assert.NoError(t, err)
				gotValue := helper.ReadCgroupFileContentsInt(parentDir, cpuIdle)
				assert.NotNil(t, gotValue)
				assert.Equal(t, wantValue, *gotValue)
			}
		})
	}
}

func testGetDisabledRuleParam() Param {
	return Param{}
}

func testGetEnabledRule() *Rule {
	// use default CPUQOS
	return &Rule{
		podQOSParams: map[extension.QoSClass]Param{
			extension.QoSLSE: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			extension.QoSLSR: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			extension.QoSLS: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			extension.QoSBE: {
				IsPodEnabled: true,
				IsExpeller:   false,
				IsCPUIdle:    false,
			},
		},
		kubeQOSPodParams: map[corev1.PodQOSClass]Param{
			corev1.PodQOSGuaranteed: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			corev1.PodQOSBurstable: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			corev1.PodQOSBestEffort: {
				IsPodEnabled: true,
				IsExpeller:   false,
				IsCPUIdle:    false,
			},
		},
	}
}

func testGetAllEnabledRule() *Rule {
	// use default CPUQOS and enable CPU Idle
	return &Rule{
		podQOSParams: map[extension.QoSClass]Param{
			extension.QoSLSE: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			extension.QoSLSR: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			extension.QoSLS: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			extension.QoSBE: {
				IsPodEnabled: true,
				IsExpeller:   false,
				IsCPUIdle:    true,
			},
		},
		kubeQOSPodParams: map[corev1.PodQOSClass]Param{
			corev1.PodQOSGuaranteed: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			corev1.PodQOSBurstable: {
				IsPodEnabled: true,
				IsExpeller:   true,
				IsCPUIdle:    false,
			},
			corev1.PodQOSBestEffort: {
				IsPodEnabled: true,
				IsExpeller:   false,
				IsCPUIdle:    true,
			},
		},
	}
}

func testGetDisabledRule() *Rule {
	return &Rule{
		podQOSParams: map[extension.QoSClass]Param{
			extension.QoSLSE: testGetDisabledRuleParam(),
			extension.QoSLSR: testGetDisabledRuleParam(),
			extension.QoSLS:  testGetDisabledRuleParam(),
			extension.QoSBE:  testGetDisabledRuleParam(),
		},
		kubeQOSPodParams: map[corev1.PodQOSClass]Param{
			corev1.PodQOSGuaranteed: testGetDisabledRuleParam(),
			corev1.PodQOSBurstable:  testGetDisabledRuleParam(),
			corev1.PodQOSBestEffort: testGetDisabledRuleParam(),
		},
	}
}

func testGetEnabledResourceQOSPolicies() *slov1alpha1.ResourceQOSPolicies {
	cpuPolicy := slov1alpha1.CPUQOSPolicyCoreSched
	return &slov1alpha1.ResourceQOSPolicies{
		CPUPolicy: &cpuPolicy,
	}
}

func testGetContainerCgroupParentDir(t *testing.T, podParentDir string, containerID string) string {
	dir, err := util.GetContainerCgroupParentDirByID(podParentDir, containerID)
	assert.NoError(t, err)
	return dir
}
