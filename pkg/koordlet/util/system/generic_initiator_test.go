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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"
)

func Test_parseIDs(t *testing.T) {
	type args struct {
	}
	tests := []struct {
		name    string
		s       string
		want    sets.Set[int32]
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name:    "normal",
			s:       "0,1,2,3,4-9,10-11,12,13-20",
			want:    sets.New[int32](0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20),
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIDs(tt.s)
			if !tt.wantErr(t, err, fmt.Sprintf("parseIDs(%v)", tt.s)) {
				return
			}
			assert.Equalf(t, tt.want, got, "parseIDs(%v)", tt.s)
		})
	}
}
