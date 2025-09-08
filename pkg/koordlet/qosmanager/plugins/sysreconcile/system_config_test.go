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

package sysreconcile

import (
	"strconv"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
	"github.com/koordinator-sh/koordinator/pkg/util/cache"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func Test_systemConfig_reconcile(t *testing.T) {
	defaultStrategy := getTestDefaultStrategy()
	nodeValidMemory := int64(512) * 1024 * 1024 * 1024
	initNode := testutil.MockTestNode("80", strconv.FormatInt(nodeValidMemory, 10))
	tests := []struct {
		name         string
		initStrategy *slov1alpha1.SystemStrategy
		newStrategy  *slov1alpha1.SystemStrategy
		node         *corev1.Node
		expect       map[sysutil.Resource]string
	}{
		{
			name:         "testNodeNil",
			initStrategy: defaultStrategy,
			newStrategy:  &slov1alpha1.SystemStrategy{},
			expect: map[sysutil.Resource]string{
				sysutil.MinFreeKbytes:        strconv.FormatInt(nodeValidMemory/1024**defaultStrategy.MinFreeKbytesFactor/10000, 10),
				sysutil.WatermarkScaleFactor: strconv.FormatInt(*defaultStrategy.WatermarkScaleFactor, 10),
				sysutil.MemcgReapBackGround:  strconv.FormatInt(*defaultStrategy.MemcgReapBackGround, 10),
			},
		},
		{
			name:         "testNodeMemoryInvalid",
			initStrategy: defaultStrategy,
			newStrategy:  &slov1alpha1.SystemStrategy{},
			node:         testutil.MockTestNode("80", "0"),
			expect: map[sysutil.Resource]string{
				sysutil.MinFreeKbytes:        strconv.FormatInt(nodeValidMemory/1024**defaultStrategy.MinFreeKbytesFactor/10000, 10),
				sysutil.WatermarkScaleFactor: strconv.FormatInt(*defaultStrategy.WatermarkScaleFactor, 10),
				sysutil.MemcgReapBackGround:  strconv.FormatInt(*defaultStrategy.MemcgReapBackGround, 10),
			},
		},
		{
			name:         "testNil",
			initStrategy: defaultStrategy,
			newStrategy:  &slov1alpha1.SystemStrategy{},
			node:         testutil.MockTestNode("80", strconv.FormatInt(nodeValidMemory, 10)),
			expect: map[sysutil.Resource]string{
				sysutil.MinFreeKbytes:        strconv.FormatInt(nodeValidMemory/1024**defaultStrategy.MinFreeKbytesFactor/10000, 10),
				sysutil.WatermarkScaleFactor: strconv.FormatInt(*defaultStrategy.WatermarkScaleFactor, 10),
				sysutil.MemcgReapBackGround:  strconv.FormatInt(*defaultStrategy.MemcgReapBackGround, 10),
			},
		},
		{
			name:         "testInvalid",
			initStrategy: defaultStrategy,
			node:         testutil.MockTestNode("80", strconv.FormatInt(nodeValidMemory, 10)),
			newStrategy:  &slov1alpha1.SystemStrategy{MinFreeKbytesFactor: pointer.Int64(-1), WatermarkScaleFactor: pointer.Int64(-1), MemcgReapBackGround: pointer.Int64(-1)},
			expect: map[sysutil.Resource]string{
				sysutil.MinFreeKbytes:        strconv.FormatInt(nodeValidMemory/1024**defaultStrategy.MinFreeKbytesFactor/10000, 10),
				sysutil.WatermarkScaleFactor: strconv.FormatInt(*defaultStrategy.WatermarkScaleFactor, 10),
				sysutil.MemcgReapBackGround:  strconv.FormatInt(*defaultStrategy.MemcgReapBackGround, 10),
			},
		},
		{
			name:         "testTooSmall",
			initStrategy: defaultStrategy,
			node:         testutil.MockTestNode("80", strconv.FormatInt(nodeValidMemory, 10)),
			newStrategy:  &slov1alpha1.SystemStrategy{MinFreeKbytesFactor: pointer.Int64(0), WatermarkScaleFactor: pointer.Int64(5), MemcgReapBackGround: pointer.Int64(-1)},
			expect: map[sysutil.Resource]string{
				sysutil.MinFreeKbytes:        strconv.FormatInt(nodeValidMemory/1024**defaultStrategy.MinFreeKbytesFactor/10000, 10),
				sysutil.WatermarkScaleFactor: "150",
				sysutil.MemcgReapBackGround:  "0",
			},
		},
		{
			name:         "testValid",
			initStrategy: defaultStrategy,
			node:         testutil.MockTestNode("80", strconv.FormatInt(nodeValidMemory, 10)),
			newStrategy:  &slov1alpha1.SystemStrategy{MinFreeKbytesFactor: pointer.Int64(88), WatermarkScaleFactor: pointer.Int64(99), MemcgReapBackGround: pointer.Int64(1)},
			expect: map[sysutil.Resource]string{
				sysutil.MinFreeKbytes:        strconv.FormatInt(nodeValidMemory/1024*88/10000, 10),
				sysutil.WatermarkScaleFactor: "99",
				sysutil.MemcgReapBackGround:  "1",
			},
		},
		{
			name:         "testToolarge",
			initStrategy: defaultStrategy,
			node:         testutil.MockTestNode("80", strconv.FormatInt(nodeValidMemory, 10)),
			newStrategy:  &slov1alpha1.SystemStrategy{MinFreeKbytesFactor: pointer.Int64(400), WatermarkScaleFactor: pointer.Int64(500), MemcgReapBackGround: pointer.Int64(2)},
			expect: map[sysutil.Resource]string{
				sysutil.MinFreeKbytes:        strconv.FormatInt(nodeValidMemory/1024**defaultStrategy.MinFreeKbytesFactor/10000, 10),
				sysutil.WatermarkScaleFactor: "150",
				sysutil.MemcgReapBackGround:  "0",
			},
		},
		{
			name:         "skip unset parameters",
			initStrategy: sloconfig.DefaultSystemStrategy(),
			newStrategy:  &slov1alpha1.SystemStrategy{},
			node:         testutil.MockTestNode("80", strconv.FormatInt(nodeValidMemory, 10)),
			expect:       map[sysutil.Resource]string{},
		},
		{
			name:         "set part of parameters",
			initStrategy: sloconfig.DefaultSystemStrategy(),
			node:         testutil.MockTestNode("80", strconv.FormatInt(nodeValidMemory, 10)),
			newStrategy: &slov1alpha1.SystemStrategy{
				MinFreeKbytesFactor: pointer.Int64(88),
				SchedIdleSaverWmark: pointer.Int64(5000000),
				SchedFeatures: map[string]bool{
					"ID_ABSOLUTE_EXPEL": true,
				},
			},
			expect: map[sysutil.Resource]string{
				sysutil.MinFreeKbytes:       strconv.FormatInt(nodeValidMemory/1024*88/10000, 10),
				sysutil.SchedIdleSaverWmark: "5000000",
				sysutil.SchedFeatures:       "ID_ABSOLUTE_EXPEL\n",
			},
		},
		{
			name:         "set part of parameters 1",
			initStrategy: sloconfig.DefaultSystemStrategy(),
			node:         testutil.MockTestNode("80", strconv.FormatInt(nodeValidMemory, 10)),
			newStrategy: &slov1alpha1.SystemStrategy{
				SchedGroupIdentityEnabled: pointer.Int64(0),
				SchedIdleSaverWmark:       pointer.Int64(5000000),
				SchedFeatures: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": false,
				},
			},
			expect: map[sysutil.Resource]string{
				sysutil.SchedGroupIdentityEnabled: "0",
				sysutil.SchedIdleSaverWmark:       "5000000",
				sysutil.SchedFeatures:             "NO_ID_EXPELLER_SHARE_CORE\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			prepareFiles(helper, tt.initStrategy, initNode.Status.Capacity.Memory().Value())

			//prepareData: metaService pods node
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			mockstatesinformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockstatesinformer.EXPECT().GetNode().Return(tt.node).AnyTimes()
			mockstatesinformer.EXPECT().GetNodeSLO().Return(getNodeSLOBySystemStrategy(tt.newStrategy)).AnyTimes()

			reconcile := &systemConfig{
				statesInformer: mockstatesinformer,
				executor: &resourceexecutor.ResourceUpdateExecutorImpl{
					Config:        resourceexecutor.NewDefaultConfig(),
					ResourceCache: cache.NewCacheDefault(),
				},
			}
			stopCh := make(chan struct{})
			defer func() {
				close(stopCh)
			}()
			reconcile.executor.Run(stopCh)

			reconcile.reconcile()
			for file, expectValue := range tt.expect {
				got := helper.ReadFileContents(file.Path(""))
				assert.Equal(t, expectValue, got, file.Path(""))
			}
		})
	}
}

func prepareFiles(helper *sysutil.FileTestUtil, strategy *slov1alpha1.SystemStrategy, nodeMemory int64) {
	helper.CreateFile(sysutil.MinFreeKbytes.Path(""))
	if strategy.MinFreeKbytesFactor != nil {
		helper.WriteFileContents(sysutil.MinFreeKbytes.Path(""), strconv.FormatInt(*strategy.MinFreeKbytesFactor*nodeMemory/1024/10000, 10))
	}
	helper.CreateFile(sysutil.WatermarkScaleFactor.Path(""))
	if strategy.WatermarkScaleFactor != nil {
		helper.WriteFileContents(sysutil.WatermarkScaleFactor.Path(""), strconv.FormatInt(*strategy.WatermarkScaleFactor, 10))
	}
	helper.CreateFile(sysutil.MemcgReapBackGround.Path(""))
	if strategy.MemcgReapBackGround != nil {
		helper.WriteFileContents(sysutil.MemcgReapBackGround.Path(""), strconv.FormatInt(*strategy.MemcgReapBackGround, 10))
	}
	helper.CreateProcSubFile("sys/kernel/sched_group_identity_enabled")
	if strategy.SchedGroupIdentityEnabled != nil {
		helper.WriteFileContents(sysutil.SchedGroupIdentityEnabled.Path(""), strconv.FormatInt(*strategy.SchedGroupIdentityEnabled, 10))
	}
	helper.CreateProcSubFile("sys/kernel/sched_idle_saver_wmark")
	if strategy.SchedIdleSaverWmark != nil {
		helper.WriteFileContents(sysutil.SchedIdleSaverWmark.Path(""), strconv.FormatInt(*strategy.SchedIdleSaverWmark, 10))
	}
	helper.CreateFile(sysutil.SchedFeatures.Path(""))
}

func getNodeSLOBySystemStrategy(strategy *slov1alpha1.SystemStrategy) *slov1alpha1.NodeSLO {
	return &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			SystemStrategy: strategy,
		},
	}
}

func getTestDefaultStrategy() *slov1alpha1.SystemStrategy {
	return &slov1alpha1.SystemStrategy{
		MinFreeKbytesFactor:   pointer.Int64(100),
		WatermarkScaleFactor:  pointer.Int64(150),
		MemcgReapBackGround:   pointer.Int64(0),
		TotalNetworkBandwidth: resource.MustParse("0"),
	}
}
