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
	"path/filepath"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_NewColdPageCollector(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	system.Conf.SysRootDir = filepath.Join(helper.TempDir, system.Conf.SysRootDir)
	metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
		TSDBPath:              t.TempDir(),
		TSDBEnablePromMetrics: false,
	})
	defer func() {
		err = metricCache.Close()
		assert.NoError(t, err)
	}()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
	opt := &framework.Options{
		Config: &framework.Config{
			CollectResUsedInterval: 1 * time.Second,
		},
		CgroupReader:   resourceexecutor.NewCgroupReader(),
		StatesInformer: statesInformer,
		MetricCache:    metricCache,
	}
	type args struct {
		contcontentKidledScanPeriodInSecondsent string
		contentKidledUseHierarchy               string
	}
	tests := []struct {
		name       string
		args       args
		want       framework.Collector
		wantEnable bool
	}{
		{
			name: "support kidled cold page collector",
			args: args{contcontentKidledScanPeriodInSecondsent: "120", contentKidledUseHierarchy: "1"},
			want: &kidledcoldPageCollector{
				collectInterval: opt.Config.CollectResUsedInterval,
				cgroupReader:    opt.CgroupReader,
				statesInformer:  opt.StatesInformer,
				podFilter:       framework.DefaultPodFilter,
				appendableDB:    opt.MetricCache,
				metricDB:        opt.MetricCache,
				started:         atomic.NewBool(false),
			},
			wantEnable: true,
		},
		{
			name:       "don't support cold page collector and return nonCollector",
			args:       args{contcontentKidledScanPeriodInSecondsent: "0", contentKidledUseHierarchy: "-1"},
			want:       &nonColdPageCollector{},
			wantEnable: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper.WriteFileContents(system.KidledScanPeriodInSeconds.Path(""), tt.args.contcontentKidledScanPeriodInSecondsent)
			helper.WriteFileContents(system.KidledUseHierarchy.Path(""), tt.args.contentKidledUseHierarchy)
			got := New(opt)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantEnable, got.Enabled())
			assert.NotPanics(t, func() {
				got.Setup(&framework.Context{})
			})
		})
	}
}
