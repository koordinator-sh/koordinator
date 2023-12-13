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

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_getCookie(t *testing.T) {
	type fields struct {
		coreSchedExtended sysutil.CoreSchedExtendedInterface
	}
	type args struct {
		pids    []uint32
		groupID string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    uint64
		want1   []uint32
		wantErr bool
	}{
		{
			name: "no pid to sync",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1: 0,
				}, map[uint32]uint32{
					1: 1,
				}, map[uint32]bool{}),
			},
			args: args{
				pids: nil,
			},
			want:    0,
			want1:   nil,
			wantErr: false,
		},
		{
			name: "sync default cookie",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 0,
					1001: 0,
					1002: 0,
				}, map[uint32]uint32{
					1:    1,
					1000: 0,
					1001: 0,
					1002: 0,
				}, map[uint32]bool{
					1004: true,
				}),
			},
			args: args{
				pids: []uint32{
					1000,
					1001,
					1002,
					1003,
					1004,
				},
			},
			want:    0,
			want1:   nil,
			wantErr: false,
		},
		{
			name: "sync for multiple cookies and use the first new cookie",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 100000,
					1001: 100000,
					1002: 200000,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
				}, map[uint32]bool{
					1004: true,
				}),
			},
			args: args{
				pids: []uint32{
					1000,
					1001,
					1002,
					1003,
					1004,
				},
			},
			want: 100000,
			want1: []uint32{
				1000,
				1001,
			},
			wantErr: false,
		},
		{
			name: "all pids get failed",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1: 0,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
				}, map[uint32]bool{
					1000: true,
					1001: true,
					1002: true,
					1004: true,
				}),
			},
			args: args{
				pids: []uint32{
					1000,
					1001,
					1002,
				},
			},
			want:    0,
			want1:   nil,
			wantErr: false,
		},
		{
			name: "sync cookie correctly",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 100000,
					1001: 100000,
					1002: 100000,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
				}, map[uint32]bool{
					1004: true,
				}),
			},
			args: args{
				pids: []uint32{
					1000,
					1001,
					1002,
					1003,
					1004,
				},
			},
			want: 100000,
			want1: []uint32{
				1000,
				1001,
				1002,
			},
			wantErr: false,
		},
		{
			name: "sync cookie correctly 1",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 100000,
					1001: 100000,
					1002: 100000,
					1010: 100000,
					2000: 200000,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
					1010: 1010,
				}, map[uint32]bool{
					1001: true,
					1004: true,
				}),
			},
			args: args{
				pids: []uint32{
					1000,
					1001,
					1002,
					1003,
					1004,
					1010,
				},
			},
			want: 100000,
			want1: []uint32{
				1000,
				1002,
				1010,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			p.cse = tt.fields.coreSchedExtended
			got, got1, gotErr := p.getCookie(tt.args.pids, tt.args.groupID)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func Test_addCookie(t *testing.T) {
	type fields struct {
		coreSchedExtended sysutil.CoreSchedExtendedInterface
		nextCookieID      uint64
	}
	type args struct {
		pids    []uint32
		groupID string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    uint64
		want1   []uint32
		wantErr bool
	}{
		{
			name: "no pid to add",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1: 0,
					2: 0,
				}, map[uint32]uint32{
					1: 1,
					2: 2,
				}, map[uint32]bool{}),
			},
			args: args{
				pids: nil,
			},
			want:    0,
			want1:   nil,
			wantErr: false,
		},
		{
			name: "add cookie for pids with non-default cookie",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					2:    0,
					1000: 100000,
					1001: 100000,
				}, map[uint32]uint32{
					1:    1,
					2:    2,
					1000: 1000,
					1001: 1001,
				}, map[uint32]bool{}),
				nextCookieID: 100000,
			},
			args: args{
				pids: []uint32{
					1000,
					1001,
				},
			},
			want: 100000,
			want1: []uint32{
				1000,
				1001,
			},
			wantErr: false,
		},
		{
			name: "failed to add cookie for beginning pid",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					2:    0,
					1000: 0,
					1001: 0,
				}, map[uint32]uint32{
					1:    1,
					2:    2,
					1000: 1000,
					1001: 1001,
				}, map[uint32]bool{
					1000: true,
					1002: true,
				}),
				nextCookieID: 100000,
			},
			args: args{
				pids: []uint32{
					1000,
					1001,
					1002,
				},
			},
			want:    0,
			want1:   nil,
			wantErr: true,
		},
		{
			name: "add cookie correctly",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					2:    0,
					1000: 0,
					1001: 0,
				}, map[uint32]uint32{
					1:    1,
					2:    2,
					1000: 1000,
					1001: 1001,
				}, map[uint32]bool{
					1002: true,
				}),
				nextCookieID: 100000,
			},
			args: args{
				pids: []uint32{
					1000,
					1001,
					1002,
				},
			},
			want: 100000,
			want1: []uint32{
				1000,
				1001,
			},
			wantErr: false,
		},
		{
			name: "add cookie correctly 2",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					2:    0,
					1000: 0,
					1001: 0,
					1002: 0,
					1010: 100000,
				}, map[uint32]uint32{
					1:    1,
					2:    2,
					1000: 1000,
					1001: 1001,
					1002: 1002,
					1010: 1010,
				}, map[uint32]bool{
					1002: true,
				}),
				nextCookieID: 200000,
			},
			args: args{
				pids: []uint32{
					1000,
					1001,
					1002,
					1010,
				},
			},
			want: 200000,
			want1: []uint32{
				1000,
				1001,
				1010,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			curPID := uint32(2)
			p.cse = tt.fields.coreSchedExtended
			f := tt.fields.coreSchedExtended.(*sysutil.FakeCoreSchedExtended)
			f.SetCurPID(curPID)
			f.SetNextCookieID(tt.fields.nextCookieID)
			got, got1, gotErr := p.addCookie(tt.args.pids, tt.args.groupID)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
			got, gotErr = f.Get(sysutil.CoreSchedScopeThread, curPID)
			assert.NoError(t, gotErr)
			assert.Equal(t, uint64(0), got)
		})
	}
}

func Test_assignCookie(t *testing.T) {
	type fields struct {
		coreSchedExtended sysutil.CoreSchedExtendedInterface
	}
	type args struct {
		pids           []uint32
		siblingPIDs    []uint32
		groupID        string
		targetCookieID uint64
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []uint32
		want1   []uint32
		wantErr bool
	}{
		{
			name: "no pid to assign",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{},
					map[uint32]uint32{},
					map[uint32]bool{}),
			},
			args: args{
				groupID:        "1",
				targetCookieID: 100000,
			},
			want:    nil,
			want1:   nil,
			wantErr: false,
		},
		{
			name: "all pid unknown",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 100000,
					1001: 100000,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
				}, map[uint32]bool{
					1000: true,
					1001: true,
					1002: true,
				}),
			},
			args: args{
				pids: []uint32{
					1001,
					1002,
				},
				siblingPIDs: []uint32{
					1000,
				},
				groupID:        "1",
				targetCookieID: 100000,
			},
			want:    nil,
			want1:   nil,
			wantErr: false,
		},
		{
			name: "no valid sibling pid to share",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 100000,
					1001: 100000,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
				}, map[uint32]bool{
					1000: true,
				}),
			},
			args: args{
				pids: []uint32{
					1001,
					1002,
				},
				siblingPIDs: []uint32{
					1000,
				},
				groupID:        "1",
				targetCookieID: 100000,
			},
			want: nil,
			want1: []uint32{
				1000,
			},
			wantErr: true,
		},
		{
			name: "assign pid successfully",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 100000,
					1001: 0,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
				}, map[uint32]bool{
					1002: true,
				}),
			},
			args: args{
				pids: []uint32{
					1001,
					1002,
				},
				siblingPIDs: []uint32{
					1000,
				},
				groupID:        "1",
				targetCookieID: 100000,
			},
			want: []uint32{
				1001,
			},
			want1:   nil,
			wantErr: false,
		},
		{
			name: "assign pid successfully 1",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 100000,
					1001: 0,
					1002: 0,
					1003: 0,
					1010: 100000,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
					1003: 1003,
					1010: 1010,
				}, map[uint32]bool{
					1002: true,
				}),
			},
			args: args{
				pids: []uint32{
					1001,
					1002,
					1003,
					1010,
				},
				siblingPIDs: []uint32{
					1000,
				},
				groupID:        "1",
				targetCookieID: 100000,
			},
			want: []uint32{
				1001,
				1003,
				1010,
			},
			want1:   nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			p.cse = tt.fields.coreSchedExtended
			got, got1, gotErr := p.assignCookie(tt.args.pids, tt.args.siblingPIDs, tt.args.groupID, tt.args.targetCookieID)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func Test_clearCookie(t *testing.T) {
	type fields struct {
		coreSchedExtended sysutil.CoreSchedExtendedInterface
	}
	type args struct {
		pids         []uint32
		groupID      string
		lastCookieID uint64
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []uint32
	}{
		{
			name: "no pid to clear",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{},
					map[uint32]uint32{},
					map[uint32]bool{}),
			},
			args: args{
				groupID:      "1",
				lastCookieID: 100000,
			},
			want: nil,
		},
		{
			name: "all pid unknown",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 100000,
					1001: 100000,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
				}, map[uint32]bool{
					1000: true,
					1001: true,
					1002: true,
				}),
			},
			args: args{
				pids: []uint32{
					1001,
					1002,
				},
				groupID:      "1",
				lastCookieID: 100000,
			},
			want: nil,
		},
		{
			name: "clear pid correctly",
			fields: fields{
				coreSchedExtended: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:    0,
					1000: 100000,
					1001: 100000,
				}, map[uint32]uint32{
					1:    1,
					1000: 1000,
					1001: 1001,
					1002: 1002,
				}, map[uint32]bool{
					1002: true,
				}),
			},
			args: args{
				pids: []uint32{
					1001,
					1002,
				},
				groupID:      "1",
				lastCookieID: 100000,
			},
			want: []uint32{
				1001,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			p.cse = tt.fields.coreSchedExtended
			got := p.clearCookie(tt.args.pids, tt.args.groupID, tt.args.lastCookieID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_isPodEnabled(t *testing.T) {
	type field struct {
		rule *Rule
	}
	type args struct {
		podAnnotations map[string]string
		podLabels      map[string]string
		podKubeQOS     corev1.PodQOSClass
		podUID         string
	}
	tests := []struct {
		name  string
		field field
		args  args
		want  bool
		want1 string
	}{
		{
			name: "pod enabled on annotation",
			field: field{
				rule: testGetEnabledRule(),
			},
			args: args{
				podAnnotations: map[string]string{
					slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
				},
				podLabels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSLS),
				},
				podUID: "xxx",
			},
			want:  true,
			want1: "group-xxx-expeller",
		},
		{
			name: "pod enabled on annotation 1",
			field: field{
				rule: testGetDisabledRule(),
			},
			args: args{
				podAnnotations: map[string]string{
					slov1alpha1.AnnotationCoreSchedGroupID: "",
				},
				podUID: "xxx",
			},
			want:  true,
			want1: "xxx",
		},
		{
			name: "pod disabled on annotation",
			field: field{
				rule: testGetEnabledRule(),
			},
			args: args{
				podAnnotations: map[string]string{
					slov1alpha1.AnnotationCoreSchedGroupID: slov1alpha1.CoreSchedGroupIDNone,
				},
				podUID: "xxx",
			},
			want:  false,
			want1: slov1alpha1.CoreSchedGroupIDNone,
		},
		{
			name: "pod enabled according to nodeSLO",
			field: field{
				rule: testGetEnabledRule(),
			},
			args: args{
				podKubeQOS: corev1.PodQOSBurstable,
				podUID:     "xxx",
			},
			want:  true,
			want1: "xxx-expeller",
		},
		{
			name: "pod enabled according to nodeSLO 1",
			field: field{
				rule: testGetEnabledRule(),
			},
			args: args{
				podLabels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSLS),
				},
				podAnnotations: map[string]string{},
				podKubeQOS:     corev1.PodQOSGuaranteed,
				podUID:         "xxx",
			},
			want:  true,
			want1: "xxx-expeller",
		},
		{
			name: "pod enabled according to nodeSLO 2",
			field: field{
				rule: &Rule{
					podQOSParams: map[extension.QoSClass]Param{
						extension.QoSLSE: testGetDisabledRuleParam(),
						extension.QoSLSR: testGetDisabledRuleParam(),
						extension.QoSLS: {
							IsPodEnabled: true,
							IsExpeller:   false,
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
							IsExpeller:   false,
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
			args: args{
				podLabels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSLS),
				},
				podAnnotations: map[string]string{},
				podKubeQOS:     corev1.PodQOSBurstable,
				podUID:         "xxx",
			},
			want:  true,
			want1: "xxx",
		},
		{
			name: "pod disabled according to nodeSLO",
			field: field{
				rule: testGetDisabledRule(),
			},
			args: args{
				podKubeQOS: corev1.PodQOSBestEffort,
				podUID:     "xxx",
			},
			want:  false,
			want1: "xxx",
		},
		{
			name: "pod disabled according to nodeSLO 1",
			field: field{
				rule: &Rule{
					podQOSParams: map[extension.QoSClass]Param{
						extension.QoSLSE: testGetDisabledRuleParam(),
						extension.QoSLSR: testGetDisabledRuleParam(),
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
			args: args{
				podLabels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSLSR),
				},
				podAnnotations: map[string]string{},
				podKubeQOS:     corev1.PodQOSGuaranteed,
				podUID:         "xxx",
			},
			want:  false,
			want1: "xxx",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				rule: tt.field.rule,
			}
			got, got1 := p.getPodEnabledAndGroup(tt.args.podAnnotations, tt.args.podLabels, tt.args.podKubeQOS, tt.args.podUID)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func Test_getContainerPIDs(t *testing.T) {
	type fields struct {
		prepareFn   func(helper *sysutil.FileTestUtil)
		useCgroupV2 bool
	}
	tests := []struct {
		name    string
		fields  fields
		arg     string
		want    []uint32
		wantErr bool
	}{
		{
			name: "get container PIDs correctly",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods/podxxxxxx/yyyyyy", sysutil.CPUProcs, "12344\n12345\n")
				},
				useCgroupV2: false,
			},
			arg: "kubepods/podxxxxxx/yyyyyy",
			want: []uint32{
				12344,
				12345,
			},
			wantErr: false,
		},
		{
			name: "aborted to get PIDs when cgroup dir not exist",
			fields: fields{
				prepareFn:   func(helper *sysutil.FileTestUtil) {},
				useCgroupV2: false,
			},
			arg:     "kubepods/podxxxxxx/yyyyyy",
			want:    nil,
			wantErr: false,
		},
		{
			name: "consider container pids as PIDs when PIDs not exist",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods/podxxxxxx/yyyyyy", sysutil.CPUProcs, "12344\n12345\n12350\n")
				},
			},
			arg: "kubepods/podxxxxxx/yyyyyy",
			want: []uint32{
				12344,
				12345,
				12350,
			},
			wantErr: false,
		},
		{
			name: "get container PIDs correctly on cgroup-v2",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods/podxxxxxx/yyyyyy", sysutil.CPUProcsV2, "12344\n12345\n12350\n")
				},
				useCgroupV2: true,
			},
			arg: "kubepods/podxxxxxx/yyyyyy",
			want: []uint32{
				12344,
				12345,
				12350,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.SetCgroupsV2(tt.fields.useCgroupV2)
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}

			p := newPlugin()
			p.Setup(hooks.Options{
				Reader: resourceexecutor.NewCgroupReader(),
			})
			got, gotErr := p.getContainerPIDs(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}
