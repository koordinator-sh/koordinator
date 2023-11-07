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

package coldmemoryresource

import (
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_kideldEnable(t *testing.T) {
	type fields struct {
		SetSysUtil func(helper *system.FileTestUtil)
		fg         map[string]bool
	}
	tests := []struct {
		name        string
		fields      fields
		wantsupport bool
		wantenable  bool
	}{
		{
			name: "os doesn't support kidled and koordlet feature-gate doesn't support kidled",
			fields: fields{
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.SetResourcesSupported(false, system.KidledScanPeriodInSeconds)
					helper.SetResourcesSupported(false, system.KidledUseHierarchy)
				},
				fg: map[string]bool{
					string(features.ColdPageCollector): false,
				},
			},
			wantsupport: false,
			wantenable:  false,
		},
		{
			name: "os doesn't support kidled and koordlet feature-gate supports kidled",
			fields: fields{
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.SetResourcesSupported(false, system.KidledScanPeriodInSeconds)
					helper.SetResourcesSupported(false, system.KidledUseHierarchy)
				},
				fg: map[string]bool{
					string(features.ColdPageCollector): true,
				},
			},
			wantsupport: false,
			wantenable:  false,
		},
		{
			name: "os supports kidled and koordlet feature-gate doesn't support kidled",
			fields: fields{
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.SetResourcesSupported(true, system.KidledScanPeriodInSeconds)
					helper.SetResourcesSupported(true, system.KidledUseHierarchy)
					helper.CreateCgroupFile("", system.KidledScanPeriodInSeconds)
					helper.CreateCgroupFile("", system.KidledUseHierarchy)
					helper.WriteFileContents(system.KidledScanPeriodInSeconds.Path(""), `120`)
					helper.WriteFileContents(system.KidledUseHierarchy.Path(""), `1`)
				},
				fg: map[string]bool{
					string(features.ColdPageCollector): false,
				},
			},
			wantsupport: true,
			wantenable:  false,
		},
		{
			name: "os supports kidled and koordlet feature-gate supports kidled",
			fields: fields{
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.SetResourcesSupported(true, system.KidledScanPeriodInSeconds)
					helper.SetResourcesSupported(true, system.KidledUseHierarchy)
					helper.CreateCgroupFile("", system.KidledScanPeriodInSeconds)
					helper.CreateCgroupFile("", system.KidledUseHierarchy)
					helper.WriteFileContents(system.KidledScanPeriodInSeconds.Path(""), `120`)
					helper.WriteFileContents(system.KidledUseHierarchy.Path(""), `1`)
				},
				fg: map[string]bool{
					string(features.ColdPageCollector): true,
				},
			},
			wantsupport: true,
			wantenable:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			features.DefaultMutableKoordletFeatureGate.SetFromMap(tt.fields.fg)
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
			collector := New(&framework.Options{
				Config: &framework.Config{
					ColdPageCollectorInterval: 1 * time.Second,
				},
				StatesInformer: statesInformer,
				MetricCache:    metricCache,
				CgroupReader:   resourceexecutor.NewCgroupReader(),
			})
			assert.Equal(t, tt.wantsupport, system.IsKidledSupport())
			assert.Equal(t, tt.wantenable, collector.Enabled())
		})
	}
}

func Test_collectColdPageInfo(t *testing.T) {
	testNow := time.Now()
	testContainerID := "containerd://123abc"
	testPodMetaDir := "kubepods.slice/kubepods-podxxxxxxxx.slice"
	testPodParentDir := "/kubepods.slice/kubepods-podxxxxxxxx.slice"
	testContainerParentDir := "/kubepods.slice/kubepods-podxxxxxxxx.slice/cri-containerd-123abc.scope"
	testHostAppParentDir := "/test-host-app/"
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
	type fields struct {
		podFilterOption       framework.PodFilter
		getPodMetas           []*statesinformer.PodMeta
		nodeSLO               *slov1alpha1.NodeSLO
		initPodLastStat       func(lastState *gocache.Cache)
		initContainerLastStat func(lastState *gocache.Cache)
		SetSysUtil            func(helper *system.FileTestUtil)
	}
	tests := []struct {
		name        string
		fields      fields
		wantstrated bool
	}{
		{
			name: "success collect node, pod and container cold page info for cgroup v1",
			fields: fields{
				podFilterOption: framework.DefaultPodFilter,
				getPodMetas: []*statesinformer.PodMeta{
					{
						CgroupDir: testPodMetaDir,
						Pod:       testPod,
					},
				},
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						HostApplications: []slov1alpha1.HostApplicationSpec{
							{
								Name: "host-app",
								CgroupPath: &slov1alpha1.CgroupPath{
									Base:         slov1alpha1.CgroupBaseTypeRoot,
									RelativePath: testHostAppParentDir,
								},
							},
						},
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
					helper.WriteFileContents(system.KidledScanPeriodInSeconds.Path(""), `120`)
					helper.WriteFileContents(system.KidledUseHierarchy.Path(""), `1`)
					helper.SetResourcesSupported(true, system.MemoryIdlePageStats)
					helper.WriteProcSubFileContents(system.ProcMemInfoName, meminfo)
					helper.WriteCgroupFileContents(testPodParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testPodParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
					helper.WriteCgroupFileContents(testContainerParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testContainerParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
					helper.WriteCgroupFileContents(testHostAppParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testHostAppParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
				},
			},
			wantstrated: true,
		},
		{
			name: "states informer nil",
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
					helper.WriteFileContents(system.KidledScanPeriodInSeconds.Path(""), `120`)
					helper.WriteFileContents(system.KidledUseHierarchy.Path(""), `1`)
					helper.SetResourcesSupported(true, system.MemoryIdlePageStats)
					helper.WriteProcSubFileContents(system.ProcMemInfoName, meminfo)
					helper.WriteCgroupFileContents(testPodParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testPodParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
					helper.WriteCgroupFileContents(testContainerParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testContainerParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
				},
			},
			wantstrated: false,
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
			if tt.name != "states informer nil" {
				statesInformer.EXPECT().HasSynced().Return(true).AnyTimes()
				statesInformer.EXPECT().GetAllPods().Return(tt.fields.getPodMetas).Times(1)
				statesInformer.EXPECT().GetNodeSLO().Return(tt.fields.nodeSLO).Times(1)
			}
			c := &kidledcoldPageCollector{
				collectInterval: 1 * time.Second,
				cgroupReader:    resourceexecutor.NewCgroupReader(),
				statesInformer:  statesInformer,
				podFilter:       framework.DefaultPodFilter,
				appendableDB:    metricCache,
				metricDB:        metricCache,
				started:         atomic.NewBool(false),
			}
			if tt.name == "states informer nil" {
				c.statesInformer = nil
			}
			assert.NotPanics(t, func() {
				c.collectColdPageInfo()
			})
			assert.Equal(t, tt.wantstrated, c.Started())
		})
	}
}

func Test_collectNodeColdPageInfo(t *testing.T) {
	// test collect success
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
	helper.SetResourcesSupported(true, system.MemoryIdlePageStats)
	helper.WriteCgroupFileContents("", system.MemoryIdlePageStats, idleInfoContentStr)
	helper.WriteProcSubFileContents(system.ProcMemInfoName, meminfo)
	kidledConfig := system.NewDefaultKidledConfig()
	c := &kidledcoldPageCollector{
		collectInterval: 5 * time.Second,
		cgroupReader:    resourceexecutor.NewCgroupReader(),
		statesInformer:  statesInformer,
		podFilter:       framework.DefaultPodFilter,
		appendableDB:    metricCache,
		metricDB:        metricCache,
		started:         atomic.NewBool(false),
		coldBoundary:    kidledConfig.KidledColdBoundary,
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
	assert.Equal(t, float64(7.33569024e+08), got1)
	assert.Equal(t, float64(3.401728e+08), got2)
	// test collect failed
	helper.WriteCgroupFileContents("", system.MemoryIdlePageStats, ``)
	helper.WriteProcSubFileContents(system.ProcMemInfoName, ``)
	t.Log(helper.ReadProcSubFileContents(system.ProcMemInfoName))
	assert.NotPanics(t, func() {
		c.collectNodeColdPageInfo()
	})
}

func Test_collectPodColdPageInfo(t *testing.T) {
	testNow := time.Now() /**/
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
					helper.WriteFileContents(system.KidledScanPeriodInSeconds.Path(""), `120`)
					helper.WriteFileContents(system.KidledUseHierarchy.Path(""), `1`)
					helper.SetResourcesSupported(true, system.MemoryIdlePageStats)
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
					helper.WriteFileContents(system.KidledScanPeriodInSeconds.Path(""), `120`)
					helper.WriteFileContents(system.KidledUseHierarchy.Path(""), `1`)
					helper.SetResourcesSupported(true, system.MemoryIdlePageStats)
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
			c := &kidledcoldPageCollector{
				collectInterval: 1 * time.Second,
				cgroupReader:    resourceexecutor.NewCgroupReader(),
				statesInformer:  statesInformer,
				podFilter:       framework.DefaultPodFilter,
				appendableDB:    metricCache,
				metricDB:        metricCache,
				started:         atomic.NewBool(false),
			}
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

func Test_kidledcoldPageCollector_collectHostAppsColdPageInfo(t *testing.T) {
	testNow := time.Now()
	timeNow = func() time.Time {
		return testNow
	}
	testParentDir := "kubepods.slice/kubepods-besteffort.slice/test-host-app"
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
	total_active_anon 340172800
	total_inactive_file 104857600
	total_active_file 0
	total_unevictable 0
	`
	type fields struct {
		getNodeSLO *slov1alpha1.NodeSLO
		SetSysUtil func(helper *system.FileTestUtil)
	}
	type wantMetric struct {
		coldPageSize    float64
		memWithHotCache float64
	}
	type wants struct {
		hostMetric map[string]wantMetric
	}
	tests := []struct {
		name    string
		fields  fields
		wants   wants
		wantErr bool
	}{
		{
			name:    "return error for nil node slo",
			fields:  fields{},
			wants:   wants{},
			wantErr: true,
		},
		{
			name: "get host app cold page metric",
			fields: fields{
				getNodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						HostApplications: []slov1alpha1.HostApplicationSpec{
							{
								Name: "test-host-app",
								CgroupPath: &slov1alpha1.CgroupPath{
									Base:         slov1alpha1.CgroupBaseTypeKubeBesteffort,
									RelativePath: "test-host-app/",
								},
							},
						},
					},
				},
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.WriteFileContents(system.KidledScanPeriodInSeconds.Path(""), `120`)
					helper.WriteFileContents(system.KidledUseHierarchy.Path(""), `1`)
					helper.SetResourcesSupported(true, system.MemoryIdlePageStats)
					helper.WriteCgroupFileContents(testParentDir, system.MemoryStat, testMemStat)
					helper.WriteCgroupFileContents(testParentDir, system.MemoryIdlePageStats, testMemoryIdlePageStatsContent)
				},
			},
			wants: wants{
				hostMetric: map[string]wantMetric{
					"test-host-app": {
						coldPageSize:    340172800,
						memWithHotCache: 209715200,
					},
				},
			},
			wantErr: false,
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
			statesInformer.EXPECT().GetNodeSLO().Return(tt.fields.getNodeSLO).Times(1)

			k := &kidledcoldPageCollector{
				collectInterval: 1 * time.Second,
				cgroupReader:    resourceexecutor.NewCgroupReader(),
				statesInformer:  statesInformer,
				appendableDB:    metricCache,
				metricDB:        metricCache,
			}
			got, err := k.collectHostAppsColdPageInfo()
			assert.Equal(t, tt.wantErr, err != nil)

			if err != nil {
				assert.Nil(t, got)
			} else {
				assert.Equal(t, len(tt.wants.hostMetric)*2, len(got))
				wantMetrics := make([]metriccache.MetricSample, 0, len(got))
				for appName, wantHostMetric := range tt.wants.hostMetric {
					wantColdPageSize, _ := metriccache.HostAppMemoryColdPageSizeMetric.GenerateSample(
						metriccache.MetricPropertiesFunc.HostApplication(appName), testNow, wantHostMetric.coldPageSize)
					wantMemoryWithHostPage, _ := metriccache.HostAppMemoryWithHotPageUsageMetric.GenerateSample(
						metriccache.MetricPropertiesFunc.HostApplication(appName), testNow, wantHostMetric.memWithHotCache)
					wantMetrics = append(wantMetrics, wantColdPageSize, wantMemoryWithHostPage)
				}
				assert.Equal(t, wantMetrics, got)
			}
		})
	}
}
