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

package noderesource

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_nodeResourceCollector(t *testing.T) {
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

	c := New(&framework.Options{
		Config: &framework.Config{
			CollectResUsedInterval: 1 * time.Second,
		},
		MetricCache: metricCache,
	})
	assert.NotNil(t, c)
	assert.True(t, c.Enabled())
	assert.NotPanics(t, func() {
		c.Setup(&framework.Context{})
	})
}

func Test_nodeResourceCollector_collectNodeResUsed(t *testing.T) {
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
	testNow := time.Now()
	timeNow = func() time.Time {
		return testNow
	}
	testLastCPUStatTime := testNow.Add(-time.Second)
	testCPUUsage := 1.0
	testUserTicks := testCPUUsage * float64(time.Second) / system.Jiffies
	// format: cpu $user $nice $system $idle $iowait $irq $softirq
	helper.WriteProcSubFileContents(system.ProcStatName, fmt.Sprintf(`cpu  %v 0 0 0 0 0 0 0 0 0
cpu0 %v 0 0 0 0 0 0 0 0 0`, int(testUserTicks), int(testUserTicks)))
	helper.WriteProcSubFileContents(system.ProcMemInfoName, `MemTotal:       1048576 kB
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
DirectMap1G:           0 kB`)

	testLastCPUStat := &framework.CPUStat{
		CPUTick:   0,
		Timestamp: testLastCPUStatTime,
	}

	c := &nodeResourceCollector{
		started:         atomic.NewBool(false),
		appendableDB:    metricCache,
		metricDB:        metricCache,
		lastNodeCPUStat: testLastCPUStat,
		deviceCollectors: map[string]framework.DeviceCollector{
			"TestDeviceCollector": &fakeDeviceCollector{},
		},
		sharedState: framework.NewSharedState(),
	}
	assert.NotNil(t, c)

	// test collect successfully
	assert.NotPanics(t, func() {
		c.collectNodeResUsed()
	})
	assert.True(t, c.Started())
	// validate collected values
	// assert collected time is less than 10s
	got, err := testGetNodeMetrics(t, c.metricDB, testNow, 5*time.Second)
	wantCPU := testCPUUsage
	assert.Equal(t, wantCPU, float64(got.Cpu().MilliValue()/1000))
	// MemTotal - MemAvailable
	wantMemory := float64(524288 * 1024)
	assert.Equal(t, wantMemory, float64(got.Memory().Value()))

	// validate in share state
	nodeCPU, nodeMemory := c.sharedState.GetNodeUsage()
	assert.NoError(t, err)
	assert.Equal(t, wantCPU, nodeCPU.Value)
	assert.Equal(t, wantMemory, nodeMemory.Value)

	// test first cpu collection
	c.lastNodeCPUStat = nil
	assert.NotPanics(t, func() {
		c.collectNodeResUsed()
	})

	// test collect failed
	helper.WriteProcSubFileContents(system.ProcStatName, ``)
	helper.WriteProcSubFileContents(system.ProcMemInfoName, ``)
	c.started = atomic.NewBool(false)
	assert.NotPanics(t, func() {
		c.collectNodeResUsed()
	})
	assert.False(t, c.Started())
}

type fakeDeviceCollector struct {
	framework.DeviceCollector
}

func (f *fakeDeviceCollector) GetNodeMetric() ([]metriccache.MetricSample, error) {
	return nil, nil
}

func (f *fakeDeviceCollector) Infos() metriccache.Devices {
	return util.GPUDevices{}
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

func testGetNodeMetrics(t *testing.T, metricCache metriccache.TSDBStorage, testNow time.Time, d time.Duration) (corev1.ResourceList, error) {
	rl := corev1.ResourceList{}
	testStart := testNow.Add(-d)
	testEnd := testNow.Add(d)
	queryParam := metriccache.QueryParam{
		Start:     &testStart,
		End:       &testEnd,
		Aggregate: metriccache.AggregationTypeAVG,
	}
	querier, err := metricCache.Querier(*queryParam.Start, *queryParam.End)
	assert.NoError(t, err)
	cpuAggregateResult, err := testQuery(querier, metriccache.NodeCPUUsageMetric, nil)
	assert.NoError(t, err)
	cpuUsed, err := cpuAggregateResult.Value(queryParam.Aggregate)
	assert.NoError(t, err)
	memAggregateResult, err := testQuery(querier, metriccache.NodeMemoryUsageMetric, nil)
	assert.NoError(t, err)
	memUsed, err := memAggregateResult.Value(queryParam.Aggregate)
	assert.NoError(t, err)
	rl[corev1.ResourceCPU] = *resource.NewMilliQuantity(int64(cpuUsed*1000), resource.DecimalSI)
	rl[corev1.ResourceMemory] = *resource.NewQuantity(int64(memUsed), resource.BinarySI)
	return rl, nil
}
