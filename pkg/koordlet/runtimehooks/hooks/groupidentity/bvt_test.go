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
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func initCPUBvt(dirWithKube string, value int64, helper *system.FileTestUtil) {
	helper.WriteCgroupFileContents(util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed), system.CPUBVTWarpNs,
		strconv.FormatInt(value, 10))
	helper.WriteCgroupFileContents(dirWithKube, system.CPUBVTWarpNs, strconv.FormatInt(value, 10))
}

func initKernelGroupIdentity(value int64, helper *system.FileTestUtil) {
	helper.WriteProcSubFileContents(filepath.Join(system.SysctlSubDir, system.KernelSchedGroupIdentityEnable), strconv.FormatInt(value, 10))
}

func getPodCPUBvt(podDirWithKube string, helper *system.FileTestUtil) int64 {
	valueStr := helper.ReadCgroupFileContents(podDirWithKube, system.CPUBVTWarpNs)
	value, _ := strconv.ParseInt(valueStr, 10, 64)
	return value
}

func Test_bvtPlugin_systemSupported(t *testing.T) {
	kubeRootDir := util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
	type fields struct {
		UseCgroupsV2            bool
		initPath                *string
		initKernelGroupIdentity bool
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "system support since bvt file exist",
			fields: fields{
				initPath: &kubeRootDir,
			},
			want: true,
		},
		{
			name:   "system not support since bvt not file exist",
			fields: fields{},
			want:   false,
		},
		{
			name: "system support since bvt kernel file exist",
			fields: fields{
				initKernelGroupIdentity: true,
			},
			want: true,
		},
		{
			name: "system not support since bvt not file exist (cgroups-v2)",
			fields: fields{
				UseCgroupsV2: true,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHelper := system.NewFileTestUtil(t)
			defer testHelper.Cleanup()
			testHelper.SetCgroupsV2(tt.fields.UseCgroupsV2)
			testHelper.SetValidateResource(false)
			if tt.fields.initPath != nil {
				initCPUBvt(*tt.fields.initPath, 0, testHelper)
			}
			if tt.fields.initKernelGroupIdentity {
				initKernelGroupIdentity(0, testHelper)
			}
			b := &bvtPlugin{}
			if got := b.SystemSupported(); got != tt.want {
				t.Errorf("SystemSupported() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestObject(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		b := Object()
		assert.NotNil(t, b)
		b1 := Object()
		assert.Equal(t, b, b1)
	})
}

func Test_bvtPlugin_Register(t *testing.T) {
	t.Run("register bvt plugin", func(t *testing.T) {
		b := &bvtPlugin{}
		b.Register(hooks.Options{})
	})
}

func Test_bvtPlugin_initSysctl(t *testing.T) {
	type fields struct {
		prepareFn                func(helper *system.FileTestUtil)
		hasKernelEnabled         *bool
		coreSchedSysctlSupported *bool
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
		wantFn  func(t *testing.T, helper *system.FileTestUtil)
	}{
		{
			name: "no need to init when sysctl not exist",
			fields: fields{
				hasKernelEnabled: nil,
			},
			wantErr: false,
		},
		{
			name: "only enable sysctl for group identity",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.WriteFileContents(system.GetProcSysFilePath(system.KernelSchedGroupIdentityEnable), "0")
				},
				hasKernelEnabled: nil,
			},
			wantErr: false,
			wantFn: func(t *testing.T, helper *system.FileTestUtil) {
				got := helper.ReadFileContents(system.GetProcSysFilePath(system.KernelSchedGroupIdentityEnable))
				assert.Equal(t, "1", got)
			},
		},
		{
			name: "enable sysctl for group identity and disable core sched",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.WriteFileContents(system.GetProcSysFilePath(system.KernelSchedGroupIdentityEnable), "0")
					helper.WriteFileContents(system.GetProcSysFilePath(system.KernelSchedCore), "1")
				},
				hasKernelEnabled: nil,
			},
			wantErr: false,
			wantFn: func(t *testing.T, helper *system.FileTestUtil) {
				got := helper.ReadFileContents(system.GetProcSysFilePath(system.KernelSchedGroupIdentityEnable))
				assert.Equal(t, "1", got)
				got = helper.ReadFileContents(system.GetProcSysFilePath(system.KernelSchedCore))
				assert.Equal(t, "0", got)
			},
		},
		{
			name: "skip enable sysctl for group identity 1",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.WriteFileContents(system.GetProcSysFilePath(system.KernelSchedGroupIdentityEnable), "1")
					helper.WriteFileContents(system.GetProcSysFilePath(system.KernelSchedCore), "0")
				},
				hasKernelEnabled: nil,
			},
			wantErr: false,
			wantFn: func(t *testing.T, helper *system.FileTestUtil) {
				got := helper.ReadFileContents(system.GetProcSysFilePath(system.KernelSchedGroupIdentityEnable))
				assert.Equal(t, "1", got)
				got = helper.ReadFileContents(system.GetProcSysFilePath(system.KernelSchedCore))
				assert.Equal(t, "0", got)
			},
		},
		{
			name: "failed to disable core sched",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.WriteFileContents(system.GetProcSysFilePath(system.KernelSchedGroupIdentityEnable), "0")
				},
				hasKernelEnabled:         pointer.Bool(true),
				coreSchedSysctlSupported: pointer.Bool(true),
			},
			wantErr: true,
			wantFn: func(t *testing.T, helper *system.FileTestUtil) {
				got := helper.ReadFileContents(system.GetProcSysFilePath(system.KernelSchedGroupIdentityEnable))
				assert.Equal(t, "0", got)
			},
		},
		{
			name: "failed to disable sysctl for group identity",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.WriteFileContents(system.GetProcSysFilePath(system.KernelSchedCore), "1")
				},
				hasKernelEnabled: pointer.Bool(true),
			},
			wantErr: true,
			wantFn: func(t *testing.T, helper *system.FileTestUtil) {
				got := helper.ReadFileContents(system.GetProcSysFilePath(system.KernelSchedCore))
				assert.Equal(t, "0", got)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}

			b := &bvtPlugin{
				hasKernelEnabled:         tt.fields.hasKernelEnabled,
				coreSchedSysctlSupported: tt.fields.coreSchedSysctlSupported,
			}
			gotErr := b.initSysctl()
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			if tt.wantFn != nil {
				tt.wantFn(t, helper)
			}
		})
	}
}

func Test_bvtPlugin_prepare(t *testing.T) {
	kubeRootDir := util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
	type fields struct {
		initPath                     *string
		initKernelGroupIdentity      bool
		initKernelGroupIdentityValue int
		rule                         *bvtRule
		hasKernelEnable              *bool
		sysSupported                 *bool
	}
	tests := []struct {
		name       string
		fields     fields
		want       *bvtRule
		wantFields *int64
	}{
		{
			name:   "cannot prepare since system not support",
			fields: fields{},
			want:   nil,
		},
		{
			name: "prepare successfully without sysctl",
			fields: fields{
				initPath: &kubeRootDir,
				rule: &bvtRule{
					enable: false,
				},
				hasKernelEnable: pointer.Bool(false),
			},
			want: &bvtRule{
				enable: false,
			},
		},
		{
			name: "failed to prepare since rule is empty",
			fields: fields{
				sysSupported:    pointer.Bool(true),
				hasKernelEnable: pointer.Bool(true),
			},
			want: nil,
		},
		{
			name: "no need to prepare since rule and sysctl disabled",
			fields: fields{
				initKernelGroupIdentity:      true,
				initKernelGroupIdentityValue: 0,
				rule: &bvtRule{
					enable: false,
				},
				sysSupported:    pointer.Bool(true),
				hasKernelEnable: pointer.Bool(true),
			},
			want: nil,
		},
		{
			name: "need to prepare since sysctl enabled",
			fields: fields{
				initKernelGroupIdentity:      true,
				initKernelGroupIdentityValue: 1,
				rule: &bvtRule{
					enable: false,
				},
				sysSupported:    pointer.Bool(true),
				hasKernelEnable: pointer.Bool(true),
			},
			want: &bvtRule{
				enable: false,
			},
		},
		{
			name: "need to prepare since rule enabled",
			fields: fields{
				initKernelGroupIdentity:      true,
				initKernelGroupIdentityValue: 1,
				rule: &bvtRule{
					enable: true,
				},
				sysSupported:    pointer.Bool(true),
				hasKernelEnable: pointer.Bool(true),
			},
			want: &bvtRule{
				enable: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHelper := system.NewFileTestUtil(t)
			if tt.fields.initPath != nil {
				initCPUBvt(*tt.fields.initPath, 0, testHelper)
			}
			if tt.fields.initKernelGroupIdentity {
				initKernelGroupIdentity(int64(tt.fields.initKernelGroupIdentityValue), testHelper)
			}

			b := &bvtPlugin{
				rule:             tt.fields.rule,
				hasKernelEnabled: tt.fields.hasKernelEnable,
				sysSupported:     tt.fields.sysSupported,
			}
			got := b.prepare()
			assert.Equal(t, tt.want, got)
		})
	}
}
