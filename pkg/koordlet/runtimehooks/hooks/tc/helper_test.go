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

package tc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_convertToHexClassId(t *testing.T) {
	type args struct {
		major int
		minor int
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		// TODO: Add test cases.
		{
			name: "",
			args: args{
				major: 11,
				minor: 2,
			},
			want: "0x110002",
		},
		{
			name: "",
			args: args{
				major: 1,
				minor: 2222,
			},
			want: "0x12222",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, convertToHexClassId(tt.args.major, tt.args.minor), "convertToHexClassId(%v, %v)", tt.args.major, tt.args.minor)
		})
	}
}
