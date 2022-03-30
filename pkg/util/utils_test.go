package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_Int64Ptr(t *testing.T) {
	values := []int64{
		0, 10,
	}
	type args struct {
		v int64
	}
	tests := []struct {
		name string
		args args
		want *int64
	}{
		{
			name: "case 0",
			args: args{v: values[0]},
			want: &values[0],
		},
		{
			name: "case 1",
			args: args{v: values[1]},
			want: &values[1],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Int64Ptr(tt.args.v)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Float64Ptr(t *testing.T) {
	values := []float64{
		0, 10.1,
	}
	type args struct {
		v float64
	}
	tests := []struct {
		name string
		args args
		want *float64
	}{
		{
			name: "case 0",
			args: args{v: values[0]},
			want: &values[0],
		},
		{
			name: "case 1",
			args: args{v: values[1]},
			want: &values[1],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Float64Ptr(tt.args.v)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_BoolPtr(t *testing.T) {
	values := []bool{true, false}
	got := BoolPtr(values[0])
	assert.Equal(t, &values[0], got)
	got1 := BoolPtr(values[1])
	assert.Equal(t, &values[1], got1)
}

func Test_MergeCfg(t *testing.T) {
	type TestingStruct struct {
		A *int64 `json:"a,omitempty"`
		B *int64 `json:"b,omitempty"`
	}
	type args struct {
		old interface{}
		new interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    interface{}
		wantErr bool
	}{
		{
			name: "throw an error if the inputs' types are not the same",
			args: args{
				old: &TestingStruct{},
				new: Int64Ptr(1),
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "throw an error if any of the inputs is not a pointer",
			args: args{
				old: TestingStruct{},
				new: TestingStruct{},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "throw an error if any of inputs is nil",
			args:    args{},
			want:    nil,
			wantErr: true,
		},
		{
			name: "throw an error if any of inputs is nil 1",
			args: args{
				old: &TestingStruct{},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "new is empty",
			args: args{
				old: &TestingStruct{
					A: Int64Ptr(0),
					B: Int64Ptr(1),
				},
				new: &TestingStruct{},
			},
			want: &TestingStruct{
				A: Int64Ptr(0),
				B: Int64Ptr(1),
			},
		},
		{
			name: "old is empty",
			args: args{
				old: &TestingStruct{},
				new: &TestingStruct{
					B: Int64Ptr(1),
				},
			},
			want: &TestingStruct{
				B: Int64Ptr(1),
			},
		},
		{
			name: "both are empty",
			args: args{
				old: &TestingStruct{},
				new: &TestingStruct{},
			},
			want: &TestingStruct{},
		},
		{
			name: "new one overwrites the old one",
			args: args{
				old: &TestingStruct{
					A: Int64Ptr(0),
					B: Int64Ptr(1),
				},
				new: &TestingStruct{
					B: Int64Ptr(2),
				},
			},
			want: &TestingStruct{
				A: Int64Ptr(0),
				B: Int64Ptr(2),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := MergeCfg(tt.args.old, tt.args.new)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			if !tt.wantErr {
				assert.Equal(t, tt.want, got.(*TestingStruct))
			}
		})
	}
}
