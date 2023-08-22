package util

import (
	"path/filepath"
	"testing"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/stretchr/testify/assert"
)

func Test_KidledGetIdleInfo(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	tempInvalidColdInfoPath := filepath.Join(helper.TempDir, "no_Idleinfo")
	tempColdPageInfoPath := filepath.Join(helper.TempDir, "memory.idle_page_stats")
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
	helper.WriteFileContents(tempColdPageInfoPath, idleInfoContentStr)
	type args struct {
		path string
	}
	tests := []struct {
		name    string
		args    args
		want    *ColdPageInfoByKidled
		wantErr bool
	}{
		{
			name:    "read illegal idle stat",
			args:    args{path: tempInvalidColdInfoPath},
			want:    nil,
			wantErr: true,
		},
		{
			name: "read test idle stat path",
			args: args{path: tempColdPageInfoPath},
			want: &ColdPageInfoByKidled{
				Version: "1.0", PageScans: 24, SlabScans: 0, ScanPeriodInSeconds: 120, UseHierarchy: 1, Buckets: []uint64{1, 2, 5, 15, 30, 60, 120, 240},
				Csei: []uint64{2613248, 4657152, 18182144, 293683200, 0, 0, 0, 0}, Dsei: []uint64{2568192, 5140480, 15306752, 48648192, 0, 0, 0, 0}, Cfei: []uint64{2633728, 4640768, 66531328, 340172800, 0, 0, 0, 0},
				Dfei: []uint64{0, 0, 0, 0, 0, 0, 0, 0}, Csui: []uint64{0, 0, 0, 0, 0, 0, 0, 0}, Dsui: []uint64{0, 0, 0, 0, 0, 0, 0, 0},
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
			got, gotErr := KidledColdPageInfo(tt.args.path)
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
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	tempColdPageInfoPath := filepath.Join(helper.TempDir, "memory.idle_page_stats")
	helper.WriteFileContents(tempColdPageInfoPath, coldPageInfoContentStr)
	coldPageInfo, err := KidledColdPageInfo(tempColdPageInfoPath)
	assert.NoError(t, err)
	assert.NotNil(t, coldPageInfo)
	got := coldPageInfo.GetColdPageTotalBytes()
	assert.Equal(t, uint64(949858304), got)
}
func Test_IsKidledSupported(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	content1 := `120`
	content2 := `1`
	contentinvailid1 := `-1`
	contentinvailid2 := `0`
	helper.CreateFile(KidledScanPeriodInSecondsFilePath)
	helper.CreateFile(KidledUseHierarchyFilePath)
	helper.WriteFileContents(KidledScanPeriodInSecondsFilePath, content1)
	helper.WriteFileContents(KidledUseHierarchyFilePath, content2)
	got := IsKidledSupported()
	assert.Equal(t, true, got)
	helper.WriteFileContents(KidledScanPeriodInSecondsFilePath, content1)
	helper.WriteFileContents(KidledUseHierarchyFilePath, contentinvailid2)
	got = IsKidledSupported()
	assert.Equal(t, false, got)
	helper.WriteFileContents(KidledScanPeriodInSecondsFilePath, contentinvailid1)
	helper.WriteFileContents(KidledUseHierarchyFilePath, content2)
	got = IsKidledSupported()
	assert.Equal(t, false, got)
}
