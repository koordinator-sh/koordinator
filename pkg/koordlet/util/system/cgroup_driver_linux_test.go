//go:build linux
// +build linux

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

package system

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_GuessCgroupDriverFromCgroupName(t *testing.T) {
	tests := []struct {
		name       string
		envSetup   func(cgroupRoot string)
		isCgroupV2 bool
		want       CgroupDriverType
	}{
		{
			name: "'kubepods' and 'kubepods.slice' both exist",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods"), 0755)
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods.slice"), 0755)
			},
			want: "",
		},
		{
			name:     "neither 'kubepods' nor 'kubepods.slice' exists",
			envSetup: func(cgroupRoot string) {},
			want:     "",
		},
		{
			name: "'kubepods.slice' exist",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods.slice"), 0755)
			},
			want: Systemd,
		},
		{
			name: "'kubepods' exist",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods"), 0755)
			},
			want: Cgroupfs,
		},
		{
			name: "'kubepods' exist on cgroup v2",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "kubepods"), 0755)
			},
			isCgroupV2: true,
			want:       Cgroupfs,
		},
		{
			name: "'kubepods.slice' exist on cgroup v2",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "kubepods.slice"), 0755)
			},
			isCgroupV2: true,
			want:       Systemd,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.SetCgroupsV2(tt.isCgroupV2)
			tmpCgroupRoot := helper.TempDir
			tt.envSetup(tmpCgroupRoot)
			got := GuessCgroupDriverFromCgroupName()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetCgroupFormatter(t *testing.T) {
	tests := []struct {
		name       string
		want       Formatter
		preHandle  func(cgroupRootDir string)
		isCgroupV2 bool
	}{
		{
			name:      "neither kubepods nor kubepods.slice exists",
			want:      cgroupPathFormatterInSystemd,
			preHandle: func(cgroupRootDir string) {},
		},
		{
			name: "only have kubepods dir",
			want: cgroupPathFormatterInCgroupfs,
			preHandle: func(cgroupRootDir string) {
				os.MkdirAll(filepath.Join(cgroupRootDir, "cpu", "kubepods"), 0755)
			},
		},
		{
			name: "only have kubepods.slice dir",
			want: cgroupPathFormatterInSystemd,
			preHandle: func(cgroupRootDir string) {
				os.MkdirAll(filepath.Join(cgroupRootDir, "cpu", "kubepods.slice"), 0755)
			},
		},
		{
			name: "both kubepods and kubepods.slice exists",
			want: cgroupPathFormatterInSystemd,
			preHandle: func(cgroupRootDir string) {
				os.MkdirAll(filepath.Join(cgroupRootDir, "cpu", "kubepods"), 0755)
				os.MkdirAll(filepath.Join(cgroupRootDir, "cpu", "kubepods.slice"), 0755)
			},
		},
		{
			name: "only have kubepods dir in cgroupv2",
			want: cgroupPathFormatterInCgroupfs,
			preHandle: func(cgroupRootDir string) {
				os.MkdirAll(filepath.Join(cgroupRootDir, "kubepods"), 0755)
			},
			isCgroupV2: true,
		},
		{
			name: "only have kubepods.slice dir in cgroupv2",
			want: cgroupPathFormatterInSystemd,
			preHandle: func(cgroupRootDir string) {
				os.MkdirAll(filepath.Join(cgroupRootDir, "kubepods.slice"), 0755)
			},
			isCgroupV2: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.SetCgroupsV2(tt.isCgroupV2)
			tmpCgroupRoot := helper.TempDir
			tt.preHandle(tmpCgroupRoot)

			got := GetCgroupFormatter()
			assert.Equal(t, tt.want.ParentDir, got.ParentDir)
		})
	}
}
