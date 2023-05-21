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

package metriccache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_memoryStorage_Get(t *testing.T) {
	type args struct {
		key interface{}
	}
	tests := []struct {
		name  string
		args  args
		want  interface{}
		want1 bool
	}{
		{
			name:  "exist-test1",
			args:  args{key: "test1"},
			want:  "test1",
			want1: true,
		},
		{
			name:  "not-exist-test2",
			args:  args{key: "test2"},
			want:  nil,
			want1: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := NewMemoryStorage()
			ms.Set("test1", "test1")
			got, got1 := ms.Get(tt.args.key)
			assert.Equalf(t, tt.want, got, "Get(%v)", tt.args.key)
			assert.Equalf(t, tt.want1, got1, "Get(%v)", tt.args.key)
		})
	}
}

func Test_memoryStorage_Set(t *testing.T) {
	type args struct {
		key   interface{}
		value interface{}
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "test set",
			args: args{
				key:   "test1",
				value: "test1",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := NewMemoryStorage()
			ms.Set(tt.args.key, tt.args.value)
			value, exist := ms.Get(tt.args.key)
			assert.True(t, exist)
			assert.Equal(t, tt.args.value, value)
		})
	}
}
