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

package oomscoreadj

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_getOOMScoreAdjForContainer(t *testing.T) {
	tests := []struct {
		name           string
		podAnnotations map[string]string
		containerName  string
		want           *int64
		wantErr        bool
	}{
		{
			name:           "nil annotations",
			podAnnotations: nil,
			containerName:  "c1",
			want:           nil,
			wantErr:        false,
		},
		{
			name:           "no oom score adj annotations",
			podAnnotations: map[string]string{"foo": "bar"},
			containerName:  "c1",
			want:           nil,
			wantErr:        false,
		},
		{
			name: "pod-level default applied",
			podAnnotations: map[string]string{
				extension.AnnotationOOMScoreAdj: "-500",
			},
			containerName: "c1",
			want:          ptr.To[int64](-500),
			wantErr:       false,
		},
		{
			name: "container spec overrides pod-level default",
			podAnnotations: map[string]string{
				extension.AnnotationOOMScoreAdj:     "-500",
				extension.AnnotationOOMScoreAdjSpec: `{"c1": 100}`,
			},
			containerName: "c1",
			want:          ptr.To[int64](100),
			wantErr:       false,
		},
		{
			name: "container not in spec falls back to pod-level default",
			podAnnotations: map[string]string{
				extension.AnnotationOOMScoreAdj:     "-500",
				extension.AnnotationOOMScoreAdjSpec: `{"c2": 100}`,
			},
			containerName: "c1",
			want:          ptr.To[int64](-500),
			wantErr:       false,
		},
		{
			name: "container not in spec and no pod-level default",
			podAnnotations: map[string]string{
				extension.AnnotationOOMScoreAdjSpec: `{"c2": 100}`,
			},
			containerName: "c1",
			want:          nil,
			wantErr:       false,
		},
		{
			name: "invalid spec JSON",
			podAnnotations: map[string]string{
				extension.AnnotationOOMScoreAdjSpec: `{invalid}`,
			},
			containerName: "c1",
			want:          nil,
			wantErr:       true,
		},
		{
			name: "invalid pod-level default value",
			podAnnotations: map[string]string{
				extension.AnnotationOOMScoreAdj: "not-a-number",
			},
			containerName: "c1",
			want:          nil,
			wantErr:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := getOOMScoreAdjForContainer(tt.podAnnotations, tt.containerName)
			assert.Equal(t, tt.wantErr, gotErr != nil, "unexpected error status, err: %v", gotErr)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				assert.NotNil(t, got)
				assert.Equal(t, *tt.want, *got)
			}
		})
	}
}

func Test_setOOMScoreAdjForPIDs(t *testing.T) {
	tests := []struct {
		name        string
		pidToVal    map[uint32]int64
		pidToErr    map[uint32]bool
		pids        []uint32
		target      int64
		wantUpdated int
		wantSkipped int
		wantVals    map[uint32]int64
	}{
		{
			name:        "no pid",
			pids:        nil,
			target:      -500,
			wantUpdated: 0,
			wantSkipped: 0,
		},
		{
			name:        "all pids updated",
			pidToVal:    map[uint32]int64{12: 0, 13: 100},
			pids:        []uint32{12, 13},
			target:      -500,
			wantUpdated: 2,
			wantSkipped: 0,
			wantVals:    map[uint32]int64{12: -500, 13: -500},
		},
		{
			name:        "diff-aware skips matched pid",
			pidToVal:    map[uint32]int64{12: 0, 13: -500},
			pids:        []uint32{12, 13},
			target:      -500,
			wantUpdated: 1,
			wantSkipped: 0,
			wantVals:    map[uint32]int64{12: -500, 13: -500},
		},
		{
			name:        "error pid skipped without blocking others",
			pidToVal:    map[uint32]int64{12: 0, 13: 0},
			pidToErr:    map[uint32]bool{12: true},
			pids:        []uint32{12, 13},
			target:      -500,
			wantUpdated: 1,
			wantSkipped: 1,
			wantVals:    map[uint32]int64{12: 0, 13: -500},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := sysutil.NewFakeOOMScoreAdj(tt.pidToVal, tt.pidToErr)
			gotUpdated, gotSkipped := setOOMScoreAdjForPIDs(fake, tt.pids, tt.target)
			assert.Equal(t, tt.wantUpdated, gotUpdated, "updated count")
			assert.Equal(t, tt.wantSkipped, gotSkipped, "skipped count")
			for pid, wantVal := range tt.wantVals {
				gotVal, err := fake.Get(pid)
				if tt.pidToErr[pid] {
					assert.Error(t, err)
					continue
				}
				assert.NoError(t, err)
				assert.Equal(t, wantVal, gotVal, "pid %d value", pid)
			}
		})
	}
}
