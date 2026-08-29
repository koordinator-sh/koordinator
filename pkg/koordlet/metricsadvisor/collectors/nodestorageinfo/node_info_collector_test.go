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

package nodestorageinfo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
)

func TestNew(t *testing.T) {
	metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
		TSDBPath:              t.TempDir(),
		TSDBEnablePromMetrics: false,
	})
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, metricCache.Close())
	}()

	opt := &framework.Options{
		Config: &framework.Config{
			CollectNodeStorageInfoInterval: 60 * time.Second,
		},
		MetricCache: metricCache,
	}
	c := New(opt)
	assert.NotNil(t, c)

	collector := c.(*nodeInfoCollector)
	assert.Equal(t, 60*time.Second, collector.collectInterval)
	assert.False(t, collector.started.Load())
}

func TestNodeStorageInfoCollector_Enabled(t *testing.T) {
	metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
		TSDBPath:              t.TempDir(),
		TSDBEnablePromMetrics: false,
	})
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, metricCache.Close())
	}()

	c := New(&framework.Options{
		Config:      &framework.Config{CollectNodeStorageInfoInterval: time.Second},
		MetricCache: metricCache,
	})

	// BlkIOReconcile is Alpha/disabled by default.
	assert.False(t, c.Enabled())

	// Enable BlkIOReconcile and verify Enabled() flips.
	enabled := features.DefaultKoordletFeatureGate.Enabled(features.BlkIOReconcile)
	err = features.DefaultMutableKoordletFeatureGate.SetFromMap(map[string]bool{string(features.BlkIOReconcile): true})
	assert.NoError(t, err)
	defer func() {
		_ = features.DefaultMutableKoordletFeatureGate.SetFromMap(map[string]bool{string(features.BlkIOReconcile): enabled})
	}()
	assert.True(t, c.Enabled())
}

func TestNodeStorageInfoCollector_collectNodeLocalStorageInfo(t *testing.T) {
	metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
		TSDBPath:              t.TempDir(),
		TSDBEnablePromMetrics: false,
	})
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, metricCache.Close())
	}()

	c := &nodeInfoCollector{
		collectInterval: 60 * time.Second,
		storage:         metricCache,
		started:         atomic.NewBool(false),
	}
	c.Setup(nil)
	assert.False(t, c.Started())

	// collectNodeLocalStorageInfo shells out to lsblk/vgs/lvs/findmnt. Whether those
	// commands succeed depends on the environment; the function must not panic either way.
	assert.NotPanics(t, func() {
		c.collectNodeLocalStorageInfo()
	})
}
