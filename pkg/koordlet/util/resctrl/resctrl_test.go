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

package resctrl

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetNewTaskIds(t *testing.T) {
	type args struct {
		ids      []int32
		tasksMap map[int32]struct{}
	}
	tests := []struct {
		name    string
		args    args
		want    []int32
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "tasksMap is nil",
			args: args{
				ids:      []int32{1, 2, 3},
				tasksMap: nil,
			},
			want:    []int32{1, 2, 3},
			wantErr: assert.NoError,
		},
		{
			name: "taskMap is not nil",
			args: args{
				ids: []int32{1, 2, 3},
				tasksMap: map[int32]struct{}{
					1: {},
				},
			},
			want:    []int32{2, 3},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetNewTaskIds(tt.args.ids, tt.args.tasksMap)
			if !tt.wantErr(t, err, fmt.Sprintf("GetNewTaskIds(%v, %v)", tt.args.ids, tt.args.tasksMap)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetNewTaskIds(%v, %v)", tt.args.ids, tt.args.tasksMap)
		})
	}
}
