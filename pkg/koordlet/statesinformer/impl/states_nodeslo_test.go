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

package impl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func Test_mergeNodeSLOSpec(t *testing.T) {
	testingCustomNodeSLOSpec := slov1alpha1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
			CPUSuppressThresholdPercent: pointer.Int64(80),
		},
		ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
			LSRClass: sloconfig.NoneResourceQOS(apiext.QoSLSR),
			LSClass:  sloconfig.NoneResourceQOS(apiext.QoSLS),
			BEClass: &slov1alpha1.ResourceQOS{
				CPUQOS: &slov1alpha1.CPUQOSCfg{
					Enable: pointer.Bool(true),
				},
				MemoryQOS: &slov1alpha1.MemoryQOSCfg{
					Enable: pointer.Bool(true),
				},
				ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
					Enable: pointer.Bool(true),
					ResctrlQOS: slov1alpha1.ResctrlQOS{
						CATRangeEndPercent: pointer.Int64(50),
					},
				},
			},
		},
		CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig:            slov1alpha1.CPUBurstConfig{},
			SharePoolThresholdPercent: nil,
		},
		SystemStrategy: &slov1alpha1.SystemStrategy{
			WatermarkScaleFactor: pointer.Int64(200),
		},
		Extensions: &slov1alpha1.ExtensionsMap{},
	}
	testingMergedNodeSLOSpec := sloconfig.DefaultNodeSLOSpecConfig()
	mergedInterface, err := util.MergeCfg(&testingMergedNodeSLOSpec, &testingCustomNodeSLOSpec)
	assert.NoError(t, err)
	testingMergedNodeSLOSpec = *mergedInterface.(*slov1alpha1.NodeSLOSpec)
	type args struct {
		nodeSLO *slov1alpha1.NodeSLO
	}
	type field struct {
		nodeSLO *slov1alpha1.NodeSLO
	}
	tests := []struct {
		name  string
		args  args
		field field
		want  *slov1alpha1.NodeSLO
	}{
		{
			name: "skip the merge if the old one is nil",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			field: field{nodeSLO: nil},
			want:  nil,
		},
		{
			name: "skip the merge if the new one is nil",
			field: field{
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			want: &slov1alpha1.NodeSLO{},
		},
		{
			name: "use default and do not panic if the new is nil",
			field: field{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: sloconfig.DefaultNodeSLOSpecConfig(),
				},
			},
			want: &slov1alpha1.NodeSLO{
				Spec: sloconfig.DefaultNodeSLOSpecConfig(),
			},
		},
		{
			name: "merge with the default",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: testingCustomNodeSLOSpec,
				},
			},
			field: field{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
							CPUSuppressThresholdPercent: pointer.Int64(100),
							MemoryEvictThresholdPercent: pointer.Int64(100),
						},
						ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
							LSRClass: &slov1alpha1.ResourceQOS{
								ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
									ResctrlQOS: slov1alpha1.ResctrlQOS{
										CATRangeStartPercent: pointer.Int64(0),
										CATRangeEndPercent:   pointer.Int64(100),
									},
								},
							},
							LSClass: &slov1alpha1.ResourceQOS{
								ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
									ResctrlQOS: slov1alpha1.ResctrlQOS{
										CATRangeStartPercent: pointer.Int64(0),
										CATRangeEndPercent:   pointer.Int64(100),
									},
								},
							},
							BEClass: &slov1alpha1.ResourceQOS{
								ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
									ResctrlQOS: slov1alpha1.ResctrlQOS{
										CATRangeStartPercent: pointer.Int64(0),
										CATRangeEndPercent:   pointer.Int64(40),
									},
								},
							},
						},
					},
				},
			},
			want: &slov1alpha1.NodeSLO{
				Spec: testingMergedNodeSLOSpec,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := nodeSLOInformer{nodeSLO: tt.field.nodeSLO}
			r.mergeNodeSLOSpec(tt.args.nodeSLO)
			assert.Equal(t, tt.want, r.nodeSLO)
		})
	}
}

func Test_createNodeSLO(t *testing.T) {
	testingNewNodeSLO := &slov1alpha1.NodeSLO{
		Spec: sloconfig.DefaultNodeSLOSpecConfig(),
	}
	testingNewNodeSLO.Spec.ResourceUsedThresholdWithBE = &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.Bool(true),
		CPUSuppressThresholdPercent: pointer.Int64(80),
	}

	testingNewNodeSLO.Spec.ResourceQOSStrategy.BEClass = &slov1alpha1.ResourceQOS{
		ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
			Enable: pointer.Bool(true),
			ResctrlQOS: slov1alpha1.ResctrlQOS{
				CATRangeStartPercent: pointer.Int64(0),
				CATRangeEndPercent:   pointer.Int64(20),
			},
		},
	}

	testingCreatedNodeSLO := &slov1alpha1.NodeSLO{
		Spec: sloconfig.DefaultNodeSLOSpecConfig(),
	}
	testingCreatedNodeSLO.Spec.ResourceUsedThresholdWithBE.Enable = pointer.Bool(true)
	testingCreatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = pointer.Int64(80)
	testingCreatedNodeSLO.Spec.ResourceQOSStrategy.LSRClass = sloconfig.NoneResourceQOS(apiext.QoSLSR)
	testingCreatedNodeSLO.Spec.ResourceQOSStrategy.LSClass = sloconfig.NoneResourceQOS(apiext.QoSLS)
	testingCreatedNodeSLO.Spec.ResourceQOSStrategy.BEClass = sloconfig.NoneResourceQOS(apiext.QoSBE)
	testingCreatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.Enable = pointer.Bool(true)
	testingCreatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.CATRangeStartPercent = pointer.Int64(0)
	testingCreatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.CATRangeEndPercent = pointer.Int64(20)

	r := nodeSLOInformer{
		nodeSLO:        nil,
		callbackRunner: NewCallbackRunner(),
	}

	r.updateNodeSLOSpec(testingNewNodeSLO)
	assert.Equal(t, testingCreatedNodeSLO, r.nodeSLO)
}

func Test_updateNodeSLOSpec(t *testing.T) {
	testingNewNodeSLO := &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.Bool(true),
				CPUSuppressThresholdPercent: pointer.Int64(80),
			},
			ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
				BEClass: &slov1alpha1.ResourceQOS{
					ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
						Enable: pointer.Bool(true),
						ResctrlQOS: slov1alpha1.ResctrlQOS{
							CATRangeStartPercent: pointer.Int64(0),
							CATRangeEndPercent:   pointer.Int64(20),
						},
					},
				},
			},
		},
	}
	testingUpdatedNodeSLO := &slov1alpha1.NodeSLO{
		Spec: sloconfig.DefaultNodeSLOSpecConfig(),
	}
	testingUpdatedNodeSLO.Spec.ResourceUsedThresholdWithBE.Enable = pointer.Bool(true)
	testingUpdatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = pointer.Int64(80)
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.LSRClass.CPUQOS.CPUQOS = *sloconfig.NoneCPUQOS()
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.LSRClass.MemoryQOS.MemoryQOS = *sloconfig.NoneMemoryQOS()
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.LSRClass.ResctrlQOS.ResctrlQOS = *sloconfig.NoneResctrlQOS()

	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.LSClass.CPUQOS.CPUQOS = *sloconfig.NoneCPUQOS()
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.LSClass.MemoryQOS.MemoryQOS = *sloconfig.NoneMemoryQOS()
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.LSClass.ResctrlQOS.ResctrlQOS = *sloconfig.NoneResctrlQOS()

	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.CPUQOS.CPUQOS = *sloconfig.NoneCPUQOS()
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.MemoryQOS.MemoryQOS = *sloconfig.NoneMemoryQOS()
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.Enable = pointer.Bool(true)
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.CATRangeStartPercent = pointer.Int64(0)
	testingUpdatedNodeSLO.Spec.ResourceQOSStrategy.BEClass.ResctrlQOS.CATRangeEndPercent = pointer.Int64(20)

	r := nodeSLOInformer{
		nodeSLO: &slov1alpha1.NodeSLO{
			Spec: slov1alpha1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64(90),
					MemoryEvictThresholdPercent: pointer.Int64(90),
				},
			},
		},
		callbackRunner: NewCallbackRunner(),
	}

	r.updateNodeSLOSpec(testingNewNodeSLO)
	assert.Equal(t, testingUpdatedNodeSLO, r.nodeSLO)
}

func Test_mergeSLOSpecResourceUsedThresholdWithBE(t *testing.T) {
	testingDefaultSpec := sloconfig.DefaultResourceThresholdStrategy()
	testingNewSpec := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.Bool(true),
		CPUSuppressThresholdPercent: pointer.Int64(80),
		MemoryEvictThresholdPercent: pointer.Int64(75),
	}
	testingNewSpec1 := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.Bool(true),
		CPUSuppressThresholdPercent: pointer.Int64(80),
	}
	testingMergedSpec := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.Bool(true),
		CPUSuppressThresholdPercent: pointer.Int64(80),
		MemoryEvictThresholdPercent: pointer.Int64(70),
		CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
		CPUEvictPolicy:              slov1alpha1.EvictByRealLimitPolicy,
	}
	type args struct {
		defaultSpec *slov1alpha1.ResourceThresholdStrategy
		newSpec     *slov1alpha1.ResourceThresholdStrategy
	}
	tests := []struct {
		name string
		args args
		want *slov1alpha1.ResourceThresholdStrategy
	}{
		{
			name: "both empty",
			args: args{
				defaultSpec: &slov1alpha1.ResourceThresholdStrategy{},
				newSpec:     &slov1alpha1.ResourceThresholdStrategy{},
			},
			want: &slov1alpha1.ResourceThresholdStrategy{},
		},
		{
			name: "totally use new",
			args: args{
				defaultSpec: &slov1alpha1.ResourceThresholdStrategy{},
				newSpec:     testingNewSpec,
			},
			want: testingNewSpec,
		},
		{
			name: "totally use new 1",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec,
			},
			want: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.Bool(true),
				CPUSuppressThresholdPercent: pointer.Int64(80),
				MemoryEvictThresholdPercent: pointer.Int64(75),
				CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
				CPUEvictPolicy:              slov1alpha1.EvictByRealLimitPolicy,
			},
		},
		{
			name: "partially use new, merging with the default",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec1,
			},
			want: testingMergedSpec,
		},
		{
			name: "new overwrite a nil",
			args: args{
				defaultSpec: testingDefaultSpec,
			},
			want: testingDefaultSpec,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeSLOSpecResourceUsedThresholdWithBE(tt.args.defaultSpec, tt.args.newSpec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_mergeSLOSpecResourceQOSStrategy(t *testing.T) {
	testingDefaultSpec := sloconfig.DefaultResourceQOSStrategy()

	testingNewSpec := testingDefaultSpec.DeepCopy()
	testingNewSpec.BEClass.MemoryQOS.WmarkRatio = pointer.Int64(0)

	testingNewSpec1 := &slov1alpha1.ResourceQOSStrategy{
		BEClass: &slov1alpha1.ResourceQOS{
			MemoryQOS: &slov1alpha1.MemoryQOSCfg{
				Enable: pointer.Bool(true),
				MemoryQOS: slov1alpha1.MemoryQOS{
					WmarkRatio: pointer.Int64(90),
				},
			},
		},
	}

	testingMergedSpec := testingDefaultSpec.DeepCopy()
	testingMergedSpec.BEClass.MemoryQOS.Enable = pointer.Bool(true)
	testingMergedSpec.BEClass.MemoryQOS.WmarkRatio = pointer.Int64(90)

	type args struct {
		defaultSpec *slov1alpha1.ResourceQOSStrategy
		newSpec     *slov1alpha1.ResourceQOSStrategy
	}
	tests := []struct {
		name string
		args args
		want *slov1alpha1.ResourceQOSStrategy
	}{
		{
			name: "both empty",
			args: args{
				defaultSpec: &slov1alpha1.ResourceQOSStrategy{},
				newSpec:     &slov1alpha1.ResourceQOSStrategy{},
			},
			want: &slov1alpha1.ResourceQOSStrategy{},
		},
		{
			name: "totally use new",
			args: args{
				defaultSpec: &slov1alpha1.ResourceQOSStrategy{},
				newSpec:     testingNewSpec,
			},
			want: testingNewSpec,
		},
		{
			name: "totally use new 1",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec,
			},
			want: testingNewSpec,
		},
		{
			name: "partially use new, merging with the default",
			args: args{
				defaultSpec: testingDefaultSpec,
				newSpec:     testingNewSpec1,
			},
			want: testingMergedSpec,
		},
		{
			name: "new overwrite a nil",
			args: args{
				defaultSpec: testingDefaultSpec,
			},
			want: testingDefaultSpec,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeSLOSpecResourceQOSStrategy(tt.args.defaultSpec, tt.args.newSpec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_mergeNoneResourceQOSIfDisabled(t *testing.T) {
	testDefault := sloconfig.DefaultResourceQOSStrategy()
	testAllNone := sloconfig.NoneResourceQOSStrategy()

	testLSMemQOSEnabled := testDefault.DeepCopy()
	testLSMemQOSEnabled.LSClass.MemoryQOS.Enable = pointer.Bool(true)
	testLSMemQOSEnabledResult := sloconfig.NoneResourceQOSStrategy()
	testLSMemQOSEnabledResult.LSClass.MemoryQOS.Enable = pointer.Bool(true)
	testLSMemQOSEnabledResult.LSClass.MemoryQOS.MemoryQOS = *sloconfig.DefaultMemoryQOS(apiext.QoSLS)

	type args struct {
		nodeCfg *slov1alpha1.NodeSLO
	}
	tests := []struct {
		name string
		args args
		want *slov1alpha1.ResourceQOSStrategy
	}{
		{
			name: "all disabled",
			args: args{
				nodeCfg: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceQOSStrategy: testDefault,
					},
				},
			},
			want: testAllNone,
		},
		{
			name: "only ls memory qos enabled",
			args: args{
				nodeCfg: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceQOSStrategy: testLSMemQOSEnabled,
					},
				},
			},
			want: testLSMemQOSEnabledResult,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeNoneResourceQOSIfDisabled(tt.args.nodeCfg.Spec.ResourceQOSStrategy)
			assert.Equal(t, tt.want, tt.args.nodeCfg.Spec.ResourceQOSStrategy)
		})
	}
}
