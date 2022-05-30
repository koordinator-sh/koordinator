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
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func initCPUBvt(dirWithKube string, value int64, helper *system.FileTestUtil) {
	helper.WriteCgroupFileContents(dirWithKube, system.CPUBVTWarpNs, strconv.FormatInt(value, 10))
}

func getPodCPUBurst(podDirWithKube string, helper *system.FileTestUtil) int64 {
	valueStr := helper.ReadCgroupFileContents(podDirWithKube, system.CPUBVTWarpNs)
	value, _ := strconv.ParseInt(valueStr, 10, 64)
	return value
}

func Test_bvtPlugin_PreRunPodSandbox(t *testing.T) {
	defaultRule := &bvtRule{
		podQOSParams: map[ext.QoSClass]int64{
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
	}
	type fields struct {
		systemSupported *bool
	}
	type args struct {
		request  *runtimeapi.RunPodSandboxHookRequest
		response *runtimeapi.RunPodSandboxHookResponse
	}
	type want struct {
		bvtValue int64
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   want
	}{
		{
			name: "set ls pod bvt",
			fields: fields{
				systemSupported: pointer.Bool(true),
			},
			args: args{
				request: &runtimeapi.RunPodSandboxHookRequest{
					Labels: map[string]string{
						ext.LabelPodQoS: string(ext.QoSLS),
					},
					CgroupParent: "kubepods/pod-guaranteed-test-uid/",
				},
				response: &runtimeapi.RunPodSandboxHookResponse{},
			},
			want: want{
				bvtValue: 2,
			},
		},
		{
			name: "set be pod bvt",
			fields: fields{
				systemSupported: pointer.Bool(true),
			},
			args: args{
				request: &runtimeapi.RunPodSandboxHookRequest{
					Labels: map[string]string{
						ext.LabelPodQoS: string(ext.QoSBE),
					},
					CgroupParent: "kubepods/besteffort/pod-besteffort-test-uid/",
				},
				response: &runtimeapi.RunPodSandboxHookResponse{},
			},
			want: want{
				bvtValue: -1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHelper := system.NewFileTestUtil(t)
			initCPUBvt(tt.args.request.CgroupParent, 0, testHelper)

			b := &bvtPlugin{
				rule:         defaultRule,
				sysSupported: tt.fields.systemSupported,
			}
			err := b.PreRunPodSandbox(tt.args.request, tt.args.response)
			assert.NoError(t, err)

			gotBvt := getPodCPUBurst(tt.args.request.CgroupParent, testHelper)
			assert.Equal(t, tt.want.bvtValue, gotBvt, "pod bvt should equal")
		})
	}
}

func Test_bvtPlugin_systemSupported(t *testing.T) {
	kubeRootDir := util.GetKubeQosRelativePath(corev1.PodQOSGuaranteed)
	type fields struct {
		initPath *string
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
	}
	for _, tt := range tests {
		testHelper := system.NewFileTestUtil(t)
		if tt.fields.initPath != nil {
			initCPUBvt(*tt.fields.initPath, 0, testHelper)
		}
		t.Run(tt.name, func(t *testing.T) {
			b := &bvtPlugin{}
			if got := b.SystemSupported(); got != tt.want {
				t.Errorf("SystemSupported() = %v, want %v", got, tt.want)
			}
		})
	}
}
