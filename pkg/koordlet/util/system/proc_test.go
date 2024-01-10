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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseProcPIDStat(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    *ProcStat
		wantErr bool
	}{
		{
			name:    "parse failed for empty input",
			arg:     "",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "parse failed for invalid comm",
			arg:     `12345 sh S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`,
			want:    nil,
			wantErr: true,
		},
		{
			name:    "parse failed for missing pid",
			arg:     `(stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`,
			want:    nil,
			wantErr: true,
		},
		{
			name:    "parse failed for invalid state",
			arg:     `12345 (stress) unknown 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`,
			want:    nil,
			wantErr: true,
		},
		{
			name:    "parse failed for invalid ppid",
			arg:     `12345 (stress) S -1 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`,
			want:    nil,
			wantErr: true,
		},
		{
			name:    "parse failed for invalid pgrp",
			arg:     `12345 (stress) S 12340 -1 12340 12300 12345 123450 151 0 0 0 0 0 ...`,
			want:    nil,
			wantErr: true,
		},
		{
			name:    "parse failed for missing fields",
			arg:     `12345 (stress) S`,
			want:    nil,
			wantErr: true,
		},
		{
			name: "parse correctly",
			arg:  `12345 (stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`,
			want: &ProcStat{
				Pid:   12345,
				Comm:  "stress",
				State: 'S',
				Ppid:  12340,
				Pgrp:  12344,
			},
			wantErr: false,
		},
		{
			name: "parse correctly 1",
			arg:  `12345 (sh stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`,
			want: &ProcStat{
				Pid:   12345,
				Comm:  "sh stress",
				State: 'S',
				Ppid:  12340,
				Pgrp:  12344,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := ParseProcPIDStat(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetPGIDForPID(t *testing.T) {
	type fields struct {
		prepareFn func(helper *FileTestUtil)
	}
	tests := []struct {
		name    string
		fields  fields
		arg     uint32
		want    uint32
		wantErr bool
	}{
		{
			name:    "get failed for /proc/<pid> not exist",
			arg:     12345,
			want:    0,
			wantErr: true,
		},
		{
			name: "get pgid failed for /proc/<pid>/stat parse failed",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					statPath := GetProcPIDStatPath(54321)
					helper.WriteFileContents(statPath, `54321 (stress) S 12340 some invalid content ...`)
				},
			},
			arg:     54321,
			want:    0,
			wantErr: true,
		},
		{
			name: "get pgid correctly",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					statPath := GetProcPIDStatPath(12345)
					helper.WriteFileContents(statPath, `12345 (stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`)
				},
			},
			arg:     12345,
			want:    12344,
			wantErr: false,
		},
		{
			name: "get pgid correctly 1",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					statPath := GetProcPIDStatPath(12345)
					helper.WriteFileContents(statPath, `12345 (sh stress) S 12340 12344 12340 12300 12345 123450 151 0 0 0 0 0 ...`)
				},
			},
			arg:     12345,
			want:    12344,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}
			got, gotErr := GetPGIDForPID(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetContainerPGIDs(t *testing.T) {
	type fields struct {
		prepareFn func(helper *FileTestUtil)
	}
	tests := []struct {
		name    string
		fields  fields
		arg     string
		want    []uint32
		wantErr bool
	}{
		{
			name:    "get failed when cgroup.procs not exist",
			arg:     "kubepods-pod12345.slice/cri-containerd-container1.scope",
			wantErr: true,
		},
		{
			name: "get failed when parse cgroup.procs failed",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					cgroupProcs, _ := GetCgroupResource(CPUProcsName)
					helper.WriteCgroupFileContents("kubepods-pod12345.slice/cri-containerd-container1.scope", cgroupProcs, "12340\n12341\n12342\ninvalid\n")
				},
			},
			arg:     "kubepods-pod12345.slice/cri-containerd-container1.scope",
			wantErr: true,
		},
		{
			name: "parse nothing for no valid pid stat",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					cgroupProcs, _ := GetCgroupResource(CPUProcsName)
					helper.WriteCgroupFileContents("kubepods-pod12345.slice/cri-containerd-container1.scope", cgroupProcs, "12340\n12342\n12345\n")
				},
			},
			arg:     "kubepods-pod12345.slice/cri-containerd-container1.scope",
			want:    nil,
			wantErr: false,
		},
		{
			name: "parse correctly",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					cgroupProcs, _ := GetCgroupResource(CPUProcsName)
					helper.WriteCgroupFileContents("kubepods-pod12345.slice/cri-containerd-container1.scope", cgroupProcs, "12340\n12342\n12345\n")
					helper.WriteProcSubFileContents("12340/stat", `12340 (bash) S 12340 12340 12340 12300 12340 123400 151 0 0 0 0 0 ...`)
					helper.WriteProcSubFileContents("12342/stat", `12342 (stress) S 12340 12340 12340 12300 12342 123450 151 0 0 0 0 0 ...`)
					helper.WriteProcSubFileContents("12345/stat", `12345 (stress) S 12342 12340 12340 12300 12345 123450 151 0 0 0 0 0 ...`)
				},
			},
			arg: "kubepods-pod12345.slice/cri-containerd-container1.scope",
			want: []uint32{
				12340,
			},
			wantErr: false,
		},
		{
			name: "parse correctly 1",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					cgroupProcs, _ := GetCgroupResource(CPUProcsName)
					helper.WriteCgroupFileContents("kubepods-pod12345.slice/cri-containerd-container1.scope", cgroupProcs, "12340\n12342\n12345\n")
					helper.WriteProcSubFileContents("12340/stat", `12340 (bash) S 12340 12340 12340 12300 12340 123400 151 0 0 0 0 0 ...`)
					helper.WriteProcSubFileContents("12342/stat", `12342 (stress) S 12340 12342 12342 12300 12342 123450 151 0 0 0 0 0 ...`)
					helper.WriteProcSubFileContents("12345/stat", `12345 (stress) S 12342 12342 12342 12300 12345 123450 151 0 0 0 0 0 ...`)
				},
			},
			arg: "kubepods-pod12345.slice/cri-containerd-container1.scope",
			want: []uint32{
				12340,
				12342,
			},
			wantErr: false,
		},
		{
			name: "parse correctly ignoring non-exist pids",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					cgroupProcs, _ := GetCgroupResource(CPUProcsName)
					helper.WriteCgroupFileContents("kubepods-pod12345.slice/cri-containerd-container1.scope", cgroupProcs, "12340\n12342\n12345\n")
					helper.WriteProcSubFileContents("12340/stat", `12340 (bash) S 12340 12340 12340 12300 12340 123400 151 0 0 0 0 0 ...`)
					helper.WriteProcSubFileContents("12342/stat", `12342 (stress) S 12340 12340 12340 12300 12342 123450 151 0 0 0 0 0 ...`)
				},
			},
			arg: "kubepods-pod12345.slice/cri-containerd-container1.scope",
			want: []uint32{
				12340,
			},
			wantErr: false,
		},
		{
			name: "consider pid as PGID if PGID not exist",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					cgroupProcs, _ := GetCgroupResource(CPUProcsName)
					helper.WriteCgroupFileContents("kubepods-pod12345.slice/cri-containerd-container1.scope", cgroupProcs, "12340\n12342\n12345\n")
					helper.WriteProcSubFileContents("12340/stat", `12340 (bash) S 12340 12340 12340 12300 12340 123400 151 0 0 0 0 0 ...`)
					helper.WriteProcSubFileContents("12342/stat", `12342 (stress) S 12340 12340 12340 12300 12342 123450 151 0 0 0 0 0 ...`)
					helper.WriteProcSubFileContents("12345/stat", `12345 (sleep) S 12340 12344 12344 12340 12345 123460 200 0 0 0 0 0 ...`)
				},
			},
			arg: "kubepods-pod12345.slice/cri-containerd-container1.scope",
			want: []uint32{
				12340,
				12345,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}
			got, gotErr := GetContainerPGIDs(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}
