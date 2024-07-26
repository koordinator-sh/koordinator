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
					1: struct{}{},
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
