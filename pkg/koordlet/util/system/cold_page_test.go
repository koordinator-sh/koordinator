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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_IsKidledSupported(t *testing.T) {
	helper := NewFileTestUtil(t)
	defer helper.Cleanup()
	KidledScanPeriodInSecondsFilePath = filepath.Join(helper.TempDir, "scan_period_in_seconds")
	KidledUseHierarchyFilePath = filepath.Join(helper.TempDir, "use_hierarchy")

	type args struct {
		contcontentKidledScanPeriodInSecondsent string
		contentKidledUseHierarchy               string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "support kidled cold page info",
			args: args{contcontentKidledScanPeriodInSecondsent: "120", contentKidledUseHierarchy: "1"},
			want: true,
		},
		{
			name: "don't support kidled cold page info, invalid scan_period_in_seconds",
			args: args{contcontentKidledScanPeriodInSecondsent: "-1", contentKidledUseHierarchy: "1"},
			want: false,
		},
		{
			name: "don't support kidled cold page info, invalid use_hierarchy",
			args: args{contcontentKidledScanPeriodInSecondsent: "120", contentKidledUseHierarchy: "0"},
			want: false,
		},
		{
			name: "don't support kidled cold page info, invalid scan_period_in_seconds and use_hierarchy",
			args: args{contcontentKidledScanPeriodInSecondsent: "-1", contentKidledUseHierarchy: "0"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper.WriteFileContents(KidledScanPeriodInSecondsFilePath, tt.args.contcontentKidledScanPeriodInSecondsent)
			helper.WriteFileContents(KidledUseHierarchyFilePath, tt.args.contentKidledUseHierarchy)
			got := IsKidledSupported()
			assert.Equal(t, tt.want, got)
		})
	}
}
