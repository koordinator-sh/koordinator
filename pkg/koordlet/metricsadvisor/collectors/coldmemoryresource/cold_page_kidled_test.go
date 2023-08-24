package coldmemoryresource

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

func Test_collectNodeColdPageInfo(t *testing.T) {
	// test collect sucess
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
	meminfo := `MemTotal:       1048576 kB
	MemFree:          262144 kB
	MemAvailable:     524288 kB
	Buffers:               0 kB
	Cached:           262144 kB
	SwapCached:            0 kB
	Active:           524288 kB
	Inactive:         262144 kB
	Active(anon):     262144 kB
	Inactive(anon):   262144 kB
	Active(file):          0 kB
	Inactive(file):   262144 kB
	Unevictable:           0 kB
	Mlocked:               0 kB
	SwapTotal:             0 kB
	SwapFree:              0 kB
	Dirty:                 0 kB
	Writeback:             0 kB
	AnonPages:             0 kB
	Mapped:                0 kB
	Shmem:                 0 kB
	Slab:                  0 kB
	SReclaimable:          0 kB
	SUnreclaim:            0 kB
	KernelStack:           0 kB
	PageTables:            0 kB
	NFS_Unstable:          0 kB
	Bounce:                0 kB
	WritebackTmp:          0 kB
	CommitLimit:           0 kB
	Committed_AS:          0 kB
	VmallocTotal:          0 kB
	VmallocUsed:           0 kB
	VmallocChunk:          0 kB
	HardwareCorrupted:     0 kB
	AnonHugePages:         0 kB
	ShmemHugePages:        0 kB
	ShmemPmdMapped:        0 kB
	CmaTotal:              0 kB
	CmaFree:               0 kB
	HugePages_Total:       0
	HugePages_Free:        0
	HugePages_Rsvd:        0
	HugePages_Surp:        0
	Hugepagesize:          0 kB
	DirectMap4k:           0 kB
	DirectMap2M:           0 kB
	DirectMap1G:           0 kB`
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
		TSDBPath:              t.TempDir(),
		TSDBEnablePromMetrics: false,
	})
	assert.NoError(t, err)
	defer func() {
		metricCache.Close()
	}()
	statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)

	helper.CreateCgroupFile("", system.MemoryIdlePageStats)
	helper.WriteCgroupFileContents("", system.MemoryIdlePageStats, idleInfoContentStr)
	helper.WriteProcSubFileContents(system.ProcMemInfoName, meminfo)
	opt := framework.NewDefaultConfig()
	c := &kidledcoldPageCollector{
		collectInterval: opt.CollectResUsedInterval,
		cgroupReader:    resourceexecutor.NewCgroupReader(),
		statesInformer:  statesInformer,
		podFilter:       framework.DefaultPodFilter,
		appendableDB:    metricCache,
		metricDB:        metricCache,
		started:         atomic.NewBool(false),
	}
	testNow := time.Now()
	metrics, err := c.collectNodeColdPageInfo()
	assert.NoError(t, err)
	appender := c.appendableDB.Appender()
	if err := appender.Append(metrics); err != nil {
		klog.ErrorS(err, "Append node metrics error")
		return
	}

	if err := appender.Commit(); err != nil {
		klog.Warningf("Commit node metrics failed, reason: %v", err)
		return
	}
	assert.NoError(t, err)
	got1, got2 := testGetNodeMetrics(t, c.metricDB, testNow, 5*time.Second)
	assert.Equal(t, float64(18446744073419457000), got1)
	assert.Equal(t, float64(1363836928), got2)
	// test collect failed
	helper.WriteCgroupFileContents("", system.MemoryIdlePageStats, ``)
	helper.WriteProcSubFileContents(system.ProcMemInfoName, ``)
	t.Log(helper.ReadProcSubFileContents(system.ProcMemInfoName))
	_, err = c.collectNodeColdPageInfo()
	assert.NotPanics(t, func() {
		c.collectNodeColdPageInfo()
	})
}
func Test_collectPodsColdPageInfo(t *testing.T) {
	testNow := time.Now()
	testContainerID := "containerd://123abc"
	testPodMetaDir := "kubepods.slice/kubepods-podxxxxxxxx.slice"
	testPodParentDir := "/kubepods.slice/kubepods-podxxxxxxxx.slice"
	testContainerParentDir := "/kubepods.slice/kubepods-podxxxxxxxx.slice/cri-containerd-123abc.scope"
	testMemoryIdlePageStatsContent := `# version: 1.0
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
	testMemStat := `
	total_cache 104857600
	total_rss 104857600
	total_inactive_anon 104857600
	total_active_anon 0
	total_inactive_file 104857600
	total_active_file 0
	total_unevictable 0
	`
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test",
			UID:       "xxxxxxxx",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test-container",
					ContainerID: testContainerID,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			},
		},
	}
	testFailedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-failed-pod",
			Namespace: "test",
			UID:       "yyyyyy",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test-container",
					ContainerID: testContainerID,
				},
			},
		},
	}
	type fields struct {
		podFilterOption       framework.PodFilter
		getPodMetas           []*statesinformer.PodMeta
		initPodLastStat       func(lastState *gocache.Cache)
		initContainerLastStat func(lastState *gocache.Cache)
		SetSysUtil            func(helper *system.FileTestUtil)
	}
	type wantFields struct {
		podResourceMetric       bool
		containerResourceMetric bool
	}
	tests := []struct {
		name   string
		fields fields
		want   wantFields
	}{
		{
			name: "success collect pod cold page info for cgroup v1",
			fields: fields{
				podFilterOption: framework.DefaultPodFilter,
				getPodMetas: []*statesinformer.PodMeta{
					{
						CgroupDir: testPodMetaDir,
						Pod:       testPod,
					},
				},
				initPodLastStat: func(lastState *gocache.Cache) {
					lastState.Set(string(testPod.UID), framework.CPUStat{
						CPUUsage:  0,
						Timestamp: testNow.Add(-time.Second),
					}, gocache.DefaultExpiration)
				},
				initContainerLastStat: func(lastState *gocache.Cache) {
					lastState.Set(testContainerID, framework.CPUStat{
						CPUUsage:  0,
						Timestamp: testNow.Add(-time.Second),
					}, gocache.DefaultExpiration)
				},
				SetSysUtil: func(helper *system.FileTestUtil) {
					koordletutil.KidledScanPeriodInSecondsFilePath = filepath.Join(helper.TempDir, "scan_period_in_seconds")
					koordletutil.KidledUseHierarchyFilePath = filepath.Join(helper.TempDir, "use_hierarchy")
					helper.WriteFileContents(koordletutil.KidledScanPeriodInSecondsFilePath, `120`)
					helper.WriteFileContents(koordletutil.KidledUseHierarchyFilePath, `1`)
					helper.WriteCgroupFileContents(testPodParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testPodParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
					helper.WriteCgroupFileContents(testContainerParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testContainerParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
				},
			},
			want: wantFields{
				podResourceMetric:       true,
				containerResourceMetric: true,
			},
		},
		{
			name: "cgroups v1, filter non-running pods",
			fields: fields{
				podFilterOption: &framework.TerminatedPodFilter{},
				getPodMetas: []*statesinformer.PodMeta{
					{
						CgroupDir: testPodMetaDir,
						Pod:       testPod,
					},
					{
						Pod: testFailedPod,
					},
				},
				initPodLastStat: func(lastState *gocache.Cache) {
					lastState.Set(string(testPod.UID), framework.CPUStat{
						CPUUsage:  0,
						Timestamp: testNow.Add(-time.Second),
					}, gocache.DefaultExpiration)
				},
				initContainerLastStat: func(lastState *gocache.Cache) {
					lastState.Set(testContainerID, framework.CPUStat{
						CPUUsage:  0,
						Timestamp: testNow.Add(-time.Second),
					}, gocache.DefaultExpiration)
				},
				SetSysUtil: func(helper *system.FileTestUtil) {
					koordletutil.KidledScanPeriodInSecondsFilePath = filepath.Join(helper.TempDir, "scan_period_in_seconds")
					koordletutil.KidledUseHierarchyFilePath = filepath.Join(helper.TempDir, "use_hierarchy")
					helper.WriteFileContents(koordletutil.KidledScanPeriodInSecondsFilePath, `120`)
					helper.WriteFileContents(koordletutil.KidledUseHierarchyFilePath, `1`)
					helper.WriteCgroupFileContents(testPodParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testPodParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
					helper.WriteCgroupFileContents(testContainerParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testContainerParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
				},
			},
			want: wantFields{
				podResourceMetric:       true,
				containerResourceMetric: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.SetSysUtil != nil {
				tt.fields.SetSysUtil(helper)
			}

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
				TSDBPath:              t.TempDir(),
				TSDBEnablePromMetrics: false,
			})
			assert.NoError(t, err)
			defer func() {
				metricCache.Close()
			}()
			statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
			statesInformer.EXPECT().HasSynced().Return(true).AnyTimes()
			statesInformer.EXPECT().GetAllPods().Return(tt.fields.getPodMetas).Times(1)
			collector := New(&framework.Options{
				Config: &framework.Config{
					CollectResUsedInterval: 1 * time.Second,
				},
				StatesInformer: statesInformer,
				MetricCache:    metricCache,
				CgroupReader:   resourceexecutor.NewCgroupReader(),
				PodFilters: map[string]framework.PodFilter{
					CollectorName: tt.fields.podFilterOption,
				},
			})
			c := collector.(*kidledcoldPageCollector)
			assert.NotPanics(t, func() {
				c.collectPodsColdPageInfo()
			})
		})
	}

}
func testGetNodeMetrics(t *testing.T, metricCache metriccache.TSDBStorage, testNow time.Time, d time.Duration) (float64, float64) {
	testStart := testNow.Add(-d)
	testEnd := testNow.Add(d)
	queryParam := metriccache.QueryParam{
		Start:     &testStart,
		End:       &testEnd,
		Aggregate: metriccache.AggregationTypeAVG,
	}
	querier, err := metricCache.Querier(*queryParam.Start, *queryParam.End)
	assert.NoError(t, err)
	memWithHotPageCacheAggregateResult, err := testQuery(querier, metriccache.NodeMemoryWithHotPageUsageMetric, nil)
	assert.NoError(t, err)
	memWithHotPageCacheUsed, err := memWithHotPageCacheAggregateResult.Value(queryParam.Aggregate)
	assert.NoError(t, err)
	coldPageSizeAggregateResult, err := testQuery(querier, metriccache.NodeMemoryColdPageSizeMetric, nil)
	assert.NoError(t, err)
	coldPageSize, err := coldPageSizeAggregateResult.Value(queryParam.Aggregate)
	assert.NoError(t, err)
	return memWithHotPageCacheUsed, coldPageSize
}
func testQuery(querier metriccache.Querier, resource metriccache.MetricResource, properties map[metriccache.MetricProperty]string) (metriccache.AggregateResult, error) {
	queryMeta, err := resource.BuildQueryMeta(properties)
	if err != nil {
		return nil, err
	}
	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err = querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return nil, err
	}
	return aggregateResult, nil
}
