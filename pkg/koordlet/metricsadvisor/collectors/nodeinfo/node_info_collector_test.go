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

package nodeinfo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestNodeInfoCollector(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
		TSDBPath:              t.TempDir(),
		TSDBEnablePromMetrics: false,
	})
	assert.NoError(t, err)
	defer func() {
		err = metricCache.Close()
		assert.NoError(t, err)
	}()
	t.Run("test", func(t *testing.T) {
		opt := &framework.Options{
			Config: &framework.Config{
				CollectNodeCPUInfoInterval: 60 * time.Second,
			},
			MetricCache: metricCache,
		}
		c := New(opt)
		assert.NotNil(t, c)

		c.Setup(nil)
		assert.True(t, c.Enabled())
		assert.False(t, c.Started())

		collector := c.(*nodeInfoCollector)
		assert.NotPanics(t, func() {
			collector.collectNodeInfo()
		})
	})
}

func Test_collectNodeNUMAInfo(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
		TSDBPath:              t.TempDir(),
		TSDBEnablePromMetrics: false,
	})
	assert.NoError(t, err)
	defer func() {
		err = metricCache.Close()
		assert.NoError(t, err)
	}()

	numaMemInfoContentStr0 := `Node 0 MemTotal:       263432804 kB
Node 0 MemFree:        254391744 kB
Node 0 MemAvailable:   256703236 kB
Node 0 Buffers:          958096 kB
Node 0 Cached:                0 kB
Node 0 SwapCached:            0 kB
Node 0 Active:          2786012 kB
Node 0 Inactive:        2223752 kB
Node 0 Active(anon):     289488 kB
Node 0 Inactive(anon):     1300 kB
Node 0 Active(file):    2496524 kB
Node 0 Inactive(file):  2222452 kB
Node 0 Unevictable:           0 kB
Node 0 Mlocked:               0 kB
Node 0 SwapTotal:             0 kB
Node 0 SwapFree:              0 kB
Node 0 Dirty:               624 kB
Node 0 Writeback:             0 kB
Node 0 AnonPages:        281748 kB
Node 0 Mapped:           495936 kB
Node 0 Shmem:              2340 kB
Node 0 Slab:            1097040 kB
Node 0 SReclaimable:     445164 kB
Node 0 SUnreclaim:       651876 kB
Node 0 KernelStack:       20944 kB
Node 0 PageTables:         7896 kB
Node 0 NFS_Unstable:          0 kB
Node 0 Bounce:                0 kB
Node 0 WritebackTmp:          0 kB
Node 0 AnonHugePages:     38912 kB
Node 0 ShmemHugePages:        0 kB
Node 0 ShmemPmdMapped:        0 kB
Node 0 HugePages_Total:       0
Node 0 HugePages_Free:        0
Node 0 HugePages_Rsvd:        0
Node 0 HugePages_Surp:        0`
	numaMemInfoContentStr1 := `Node 1 MemTotal:       263432000 kB
Node 1 MemFree:        254391744 kB
Node 1 MemAvailable:   256703236 kB
Node 1 Buffers:          958096 kB
Node 1 Cached:                0 kB
Node 1 SwapCached:            0 kB
Node 1 Active:          2786012 kB
Node 1 Inactive:        2223752 kB
Node 1 Active(anon):     289488 kB
Node 1 Inactive(anon):     1300 kB
Node 1 Active(file):    2496524 kB
Node 1 Inactive(file):  2222452 kB
Node 1 Unevictable:           0 kB
Node 1 Mlocked:               0 kB
Node 1 SwapTotal:             0 kB
Node 1 SwapFree:              0 kB
Node 1 Dirty:               624 kB
Node 1 Writeback:             0 kB
Node 1 AnonPages:        281748 kB
Node 1 Mapped:           495936 kB
Node 1 Shmem:              2340 kB
Node 1 Slab:            1097040 kB
Node 1 SReclaimable:     445164 kB
Node 1 SUnreclaim:       651876 kB
Node 1 KernelStack:       20944 kB
Node 1 PageTables:         7896 kB
Node 1 NFS_Unstable:          0 kB
Node 1 Bounce:                0 kB
Node 1 WritebackTmp:          0 kB
Node 1 AnonHugePages:     38912 kB
Node 1 ShmemHugePages:        0 kB
Node 1 ShmemPmdMapped:        0 kB
Node 1 HugePages_Total:       0
Node 1 HugePages_Free:        0
Node 1 HugePages_Rsvd:        0
Node 1 HugePages_Surp:        0`
	numaMemInfoPath0 := system.GetNUMAMemInfoPath("node0")
	helper.WriteFileContents(numaMemInfoPath0, numaMemInfoContentStr0)
	numaMemInfoPath1 := system.GetNUMAMemInfoPath("node1")
	helper.WriteFileContents(numaMemInfoPath1, numaMemInfoContentStr1)
	testMemInfo0 := &koordletutil.MemInfo{
		MemTotal: 263432804, MemFree: 254391744, MemAvailable: 256703236,
		Buffers: 958096, Cached: 0, SwapCached: 0,
		Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
		InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
		Unevictable: 0, Mlocked: 0, SwapTotal: 0,
		SwapFree: 0, Dirty: 624, Writeback: 0,
		AnonPages: 281748, Mapped: 495936, Shmem: 2340,
		Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
		KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
		Bounce: 0, WritebackTmp: 0, AnonHugePages: 38912,
		HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
		HugePages_Surp: 0,
	}
	testMemInfo1 := &koordletutil.MemInfo{
		MemTotal: 263432000, MemFree: 254391744, MemAvailable: 256703236,
		Buffers: 958096, Cached: 0, SwapCached: 0,
		Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
		InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
		Unevictable: 0, Mlocked: 0, SwapTotal: 0,
		SwapFree: 0, Dirty: 624, Writeback: 0,
		AnonPages: 281748, Mapped: 495936, Shmem: 2340,
		Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
		KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
		Bounce: 0, WritebackTmp: 0, AnonHugePages: 38912,
		HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
		HugePages_Surp: 0,
	}
	expected := &koordletutil.NodeNUMAInfo{
		NUMAInfos: []koordletutil.NUMAInfo{
			{
				NUMANodeID: 0,
				MemInfo:    testMemInfo0,
			},
			{
				NUMANodeID: 1,
				MemInfo:    testMemInfo1,
			},
		},
		MemInfoMap: map[int32]*koordletutil.MemInfo{
			0: testMemInfo0,
			1: testMemInfo1,
		},
	}
	t.Run("test", func(t *testing.T) {
		c := &nodeInfoCollector{
			collectInterval: 60 * time.Second,
			storage:         metricCache,
			started:         atomic.NewBool(false),
		}

		// test collect successfully
		err = c.collectNodeNUMAInfo()
		assert.NoError(t, err)
		nodeNUMAInfoRaw, ok := c.storage.Get(metriccache.NodeNUMAInfoKey)
		assert.True(t, ok)
		nodeNUMAInfo, ok := nodeNUMAInfoRaw.(*koordletutil.NodeNUMAInfo)
		assert.True(t, ok)
		assert.Equal(t, expected, nodeNUMAInfo)

		// test collect failed when sys files are missing
		helper.Cleanup()
		err = c.collectNodeNUMAInfo()
		assert.Error(t, err)
	})
}
