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

package util

import (
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func Test_getHostCgroupRelativePath(t *testing.T) {
	type args struct {
		hostAppSpec *slov1alpha1.HostApplicationSpec
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "nil host app",
			args: args{
				hostAppSpec: nil,
			},
			want: "",
		},
		{
			name: "ls app with no cgroup",
			args: args{
				hostAppSpec: &slov1alpha1.HostApplicationSpec{
					Name: "ls-app",
					QoS:  ext.QoSLS,
				},
			},
			want: filepath.Join(defaultHostLSCgroupDir, "ls-app"),
		},
		{
			name: "be app with no cgroup",
			args: args{
				hostAppSpec: &slov1alpha1.HostApplicationSpec{
					Name: "be-app",
					QoS:  ext.QoSBE,
				},
			},
			want: filepath.Join(defaultHostBECgroupDir, "be-app"),
		},
		{
			name: "app with cgroup root base",
			args: args{
				hostAppSpec: &slov1alpha1.HostApplicationSpec{
					Name: "test-app",
					QoS:  ext.QoSLS,
					CgroupPath: &slov1alpha1.CgroupPath{
						Base:         slov1alpha1.CgroupBaseTypeRoot,
						ParentDir:    "host-ls-app",
						RelativePath: "test-app",
					},
				},
			},
			want: filepath.Join("", "host-ls-app", "test-app"),
		},
		{
			name: "app with kubepods cgroup base",
			args: args{
				hostAppSpec: &slov1alpha1.HostApplicationSpec{
					Name: "test-app",
					QoS:  ext.QoSLS,
					CgroupPath: &slov1alpha1.CgroupPath{
						Base:         slov1alpha1.CgroupBaseTypeKubepods,
						ParentDir:    "host-ls-app",
						RelativePath: "test-app",
					},
				},
			},
			want: filepath.Join(GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "host-ls-app", "test-app"),
		},
		{
			name: "app with burstable cgroup base",
			args: args{
				hostAppSpec: &slov1alpha1.HostApplicationSpec{
					Name: "test-app",
					QoS:  ext.QoSLS,
					CgroupPath: &slov1alpha1.CgroupPath{
						Base:         slov1alpha1.CgroupBaseTypeKubeBurstable,
						ParentDir:    "host-ls-app",
						RelativePath: "test-app",
					},
				},
			},
			want: filepath.Join(GetPodQoSRelativePath(corev1.PodQOSBurstable), "host-ls-app", "test-app"),
		},
		{
			name: "app with besteffort cgroup base",
			args: args{
				hostAppSpec: &slov1alpha1.HostApplicationSpec{
					Name: "test-app",
					QoS:  ext.QoSBE,
					CgroupPath: &slov1alpha1.CgroupPath{
						Base:         slov1alpha1.CgroupBaseTypeKubeBesteffort,
						ParentDir:    "host-be-app",
						RelativePath: "test-app",
					},
				},
			},
			want: filepath.Join(GetPodQoSRelativePath(corev1.PodQOSBestEffort), "host-be-app", "test-app"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetHostAppCgroupRelativePath(tt.args.hostAppSpec); got != tt.want {
				t.Errorf("getHostCgroupRelativePath() = %v, want %v", got, tt.want)
			}
		})
	}
}
