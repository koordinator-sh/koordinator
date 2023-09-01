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

func Test_IsKidledStart(t *testing.T) {
	helper := NewFileTestUtil(t)
	defer helper.Cleanup()
	Conf.SysRootDir = filepath.Join(helper.TempDir, Conf.SysRootDir)

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
			helper.WriteFileContents(KidledScanPeriodInSeconds.Path(""), tt.args.contcontentKidledScanPeriodInSecondsent)
			helper.WriteFileContents(KidledUseHierarchy.Path(""), tt.args.contentKidledUseHierarchy)
			got := IsKidledStart()
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_ParseMemoryIdlePageStats(t *testing.T) {
	idleInfoContentStr := `# version: 1.0
	# page_scans: 24
	# slab_scans: 0
	# scan_period_in_seconds: 120
	# use_hierarchy: 1
	# buckets: 1,2,5,15,30,60,120,240
	#
	#   _-----=> clean/dirty
	#  / _----=> swap/file
	# | / _---=> evict/unevict
	# || / _--=> inactive/active
	# ||| / _-=> slab
	# |||| /
	# |||||             [1,2)          [2,5)         [5,15)        [15,30)        [30,60)       [60,120)      [120,240)     [240,+inf)
	  csei            2613248        4657152       18182144      293683200              0              0              0              0
	  dsei            2568192        5140480       15306752       48648192              0              0              0              0
	  cfei            2633728        4640768       66531328      340172800              0              0              0              0
	  dfei                  0              0           4096              0              0              0              0              0
	  csui                  0              0              0              0              0              0              0              0
	  dsui                  0              0              0              0              0              0              0              0
	  cfui                  0              0              0              0              0              0              0              0
	  dfui                  0              0              0              0              0              0              0              0
	  csea             765952        1044480        3784704       52834304              0              0              0              0
	  dsea             286720         270336        1564672        5390336              0              0              0              0
	  cfea            9273344       16609280      152109056      315121664              0              0              0              0
	  dfea                  0              0              0              0              0              0              0              0
	  csua                  0              0              0              0              0              0              0              0
	  dsua                  0              0              0              0              0              0              0              0
	  cfua                  0              0              0              0              0              0              0              0
	  dfua                  0              0              0              0              0              0              0              0
	  slab                  0              0              0              0              0              0              0              0`
	type args struct {
		content string
	}
	tests := []struct {
		name    string
		args    args
		want    *ColdPageInfoByKidled
		wantErr bool
	}{
		{
			name:    "read illegal idle stat",
			args:    args{content: ""},
			want:    nil,
			wantErr: true,
		},
		{
			name: "read test idle stat path",
			args: args{content: idleInfoContentStr},
			want: &ColdPageInfoByKidled{
				Version: "1.0", PageScans: 24, SlabScans: 0, ScanPeriodInSeconds: 120, UseHierarchy: 1, Buckets: []uint64{1, 2, 5, 15, 30, 60, 120, 240},
				Csei: []uint64{2613248, 4657152, 18182144, 293683200, 0, 0, 0, 0}, Dsei: []uint64{2568192, 5140480, 15306752, 48648192, 0, 0, 0, 0}, Cfei: []uint64{2633728, 4640768, 66531328, 340172800, 0, 0, 0, 0},
				Dfei: []uint64{0, 0, 4096, 0, 0, 0, 0, 0}, Csui: []uint64{0, 0, 0, 0, 0, 0, 0, 0}, Dsui: []uint64{0, 0, 0, 0, 0, 0, 0, 0},
				Cfui: []uint64{0, 0, 0, 0, 0, 0, 0, 0}, Dfui: []uint64{0, 0, 0, 0, 0, 0, 0, 0}, Csea: []uint64{765952, 1044480, 3784704, 52834304, 0, 0, 0, 0},
				Dsea: []uint64{286720, 270336, 1564672, 5390336, 0, 0, 0, 0}, Cfea: []uint64{9273344, 16609280, 152109056, 315121664, 0, 0, 0, 0}, Dfea: []uint64{0, 0, 0, 0, 0, 0, 0, 0},
				Csua: []uint64{0, 0, 0, 0, 0, 0, 0, 0}, Dsua: []uint64{0, 0, 0, 0, 0, 0, 0, 0}, Cfua: []uint64{0, 0, 0, 0, 0, 0, 0, 0},
				Dfua: []uint64{0, 0, 0, 0, 0, 0, 0, 0}, Slab: []uint64{0, 0, 0, 0, 0, 0, 0, 0},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := ParseMemoryIdlePageStats(tt.args.content)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}

}

func Test_GetColdPageTotalBytes(t *testing.T) {
	coldPageInfoContentStr := `# version: 1.0
	# page_scans: 24
	# slab_scans: 0
	# scan_period_in_seconds: 120
	# use_hierarchy: 1
	# buckets: 1,2,5,15,30,60,120,240
	#
	#   _-----=> clean/dirty
	#  / _----=> swap/file
	# | / _---=> evict/unevict
	# || / _--=> inactive/active
	# ||| / _-=> slab
	# |||| /
	# |||||             [1,2)          [2,5)         [5,15)        [15,30)        [30,60)       [60,120)      [120,240)     [240,+inf)
	  csei            2613248        4657152       18182144      293683200              0              0              0              0
	  dsei            2568192        5140480       15306752       48648192              0              0              0              0
	  cfei            2633728        4640768       66531328      340172800              0              0              0              0
	  dfei                  0              0           4096              0              0              0              0              0
	  csui                  0              0              0              0              0              0              0              0
	  dsui                  0              0              0              0              0              0              0              0
	  cfui                  0              0              0              0              0              0              0              0
	  dfui                  0              0              0              0              0              0              0              0
	  csea             765952        1044480        3784704       52834304              0              0              0              0
	  dsea             286720         270336        1564672        5390336              0              0              0              0
	  cfea            9273344       16609280      152109056      315121664              0              0              0              0
	  dfea                  0              0              0              0              0              0              0              0
	  csua                  0              0              0              0              0              0              0              0
	  dsua                  0              0              0              0              0              0              0              0
	  cfua                  0              0              0              0              0              0              0              0
	  dfua                  0              0              0              0              0              0              0              0
	  slab                  0              0              0              0              0              0              0              0`
	coldPageInfo, err := ParseMemoryIdlePageStats(coldPageInfoContentStr)
	assert.NoError(t, err)
	assert.NotNil(t, coldPageInfo)
	got := coldPageInfo.GetColdPageTotalBytes()
	assert.Equal(t, uint64(1363836928), got)
}