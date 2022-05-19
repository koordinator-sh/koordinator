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

package statesinformer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func Test_mergeNodeSLOSpec(t *testing.T) {
	testingCustomNodeSLOSpec := slov1alpha1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
			CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
		},
		ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
			LSR: util.NoneResourceQoS(apiext.QoSLSR),
			LS:  util.NoneResourceQoS(apiext.QoSLS),
			BE: &slov1alpha1.ResourceQoS{
				CPUQoS: &slov1alpha1.CPUQoSCfg{
					Enable: pointer.BoolPtr(true),
				},
				MemoryQoS: &slov1alpha1.MemoryQoSCfg{
					Enable: pointer.BoolPtr(true),
				},
				ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
					Enable: pointer.BoolPtr(true),
					ResctrlQoS: slov1alpha1.ResctrlQoS{
						CATRangeEndPercent: pointer.Int64Ptr(50),
					},
				},
			},
		},
		CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig:            slov1alpha1.CPUBurstConfig{},
			SharePoolThresholdPercent: nil,
		},
	}
	testingMergedNodeSLOSpec := util.DefaultNodeSLOSpecConfig()
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
					Spec: util.DefaultNodeSLOSpecConfig(),
				},
			},
			want: &slov1alpha1.NodeSLO{
				Spec: util.DefaultNodeSLOSpecConfig(),
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
							CPUSuppressThresholdPercent: pointer.Int64Ptr(100),
							MemoryEvictThresholdPercent: pointer.Int64Ptr(100),
						},
						ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
							LSR: &slov1alpha1.ResourceQoS{
								ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
									ResctrlQoS: slov1alpha1.ResctrlQoS{
										CATRangeStartPercent: pointer.Int64Ptr(0),
										CATRangeEndPercent:   pointer.Int64Ptr(100),
									},
								},
							},
							LS: &slov1alpha1.ResourceQoS{
								ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
									ResctrlQoS: slov1alpha1.ResctrlQoS{
										CATRangeStartPercent: pointer.Int64Ptr(0),
										CATRangeEndPercent:   pointer.Int64Ptr(100),
									},
								},
							},
							BE: &slov1alpha1.ResourceQoS{
								ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
									ResctrlQoS: slov1alpha1.ResctrlQoS{
										CATRangeStartPercent: pointer.Int64Ptr(0),
										CATRangeEndPercent:   pointer.Int64Ptr(40),
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
			r := statesInformer{nodeSLO: tt.field.nodeSLO}
			r.mergeNodeSLOSpec(tt.args.nodeSLO)
			assert.Equal(t, tt.want, r.nodeSLO)
		})
	}
}

func Test_createNodeSLO(t *testing.T) {
	testingNewNodeSLO := &slov1alpha1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingNewNodeSLO.Spec.ResourceUsedThresholdWithBE = &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.BoolPtr(true),
		CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
	}

	testingNewNodeSLO.Spec.ResourceQoSStrategy.BE = &slov1alpha1.ResourceQoS{
		ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
			Enable: pointer.BoolPtr(true),
			ResctrlQoS: slov1alpha1.ResctrlQoS{
				CATRangeStartPercent: pointer.Int64Ptr(0),
				CATRangeEndPercent:   pointer.Int64Ptr(20),
			},
		},
	}

	testingCreatedNodeSLO := &slov1alpha1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingCreatedNodeSLO.Spec.ResourceUsedThresholdWithBE.Enable = pointer.BoolPtr(true)
	testingCreatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = pointer.Int64Ptr(80)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.LSR = util.NoneResourceQoS(apiext.QoSLSR)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.LS = util.NoneResourceQoS(apiext.QoSLS)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.BE = util.NoneResourceQoS(apiext.QoSBE)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.Enable = pointer.BoolPtr(true)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.CATRangeStartPercent = pointer.Int64Ptr(0)
	testingCreatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.CATRangeEndPercent = pointer.Int64Ptr(20)

	r := statesInformer{nodeSLO: nil}

	r.updateNodeSLOSpec(testingNewNodeSLO)
	assert.Equal(t, testingCreatedNodeSLO, r.nodeSLO)
}

func Test_updateNodeSLOSpec(t *testing.T) {
	testingNewNodeSLO := &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.BoolPtr(true),
				CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
			},
			ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
				BE: &slov1alpha1.ResourceQoS{
					ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
						Enable: pointer.BoolPtr(true),
						ResctrlQoS: slov1alpha1.ResctrlQoS{
							CATRangeStartPercent: pointer.Int64Ptr(0),
							CATRangeEndPercent:   pointer.Int64Ptr(20),
						},
					},
				},
			},
		},
	}
	testingUpdatedNodeSLO := &slov1alpha1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingUpdatedNodeSLO.Spec.ResourceUsedThresholdWithBE.Enable = pointer.BoolPtr(true)
	testingUpdatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = pointer.Int64Ptr(80)
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LSR.CPUQoS.CPUQoS = *util.NoneCPUQoS()
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LSR.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LSR.ResctrlQoS.ResctrlQoS = *util.NoneResctrlQoS()

	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LS.CPUQoS.CPUQoS = *util.NoneCPUQoS()
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LS.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.LS.ResctrlQoS.ResctrlQoS = *util.NoneResctrlQoS()

	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.BE.CPUQoS.CPUQoS = *util.NoneCPUQoS()
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.BE.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.Enable = pointer.BoolPtr(true)
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.CATRangeStartPercent = pointer.Int64Ptr(0)
	testingUpdatedNodeSLO.Spec.ResourceQoSStrategy.BE.ResctrlQoS.CATRangeEndPercent = pointer.Int64Ptr(20)

	r := statesInformer{
		nodeSLO: &slov1alpha1.NodeSLO{
			Spec: slov1alpha1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64Ptr(90),
					MemoryEvictThresholdPercent: pointer.Int64Ptr(90),
				},
			},
		},
	}

	r.updateNodeSLOSpec(testingNewNodeSLO)
	assert.Equal(t, testingUpdatedNodeSLO, r.nodeSLO)
}

func Test_mergeSLOSpecResourceUsedThresholdWithBE(t *testing.T) {
	testingDefaultSpec := util.DefaultResourceThresholdStrategy()
	testingNewSpec := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.BoolPtr(true),
		CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
		MemoryEvictThresholdPercent: pointer.Int64Ptr(75),
	}
	testingNewSpec1 := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.BoolPtr(true),
		CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
	}
	testingMergedSpec := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.BoolPtr(true),
		CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
		MemoryEvictThresholdPercent: pointer.Int64Ptr(70),
		CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
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
				Enable:                      pointer.BoolPtr(true),
				CPUSuppressThresholdPercent: pointer.Int64Ptr(80),
				MemoryEvictThresholdPercent: pointer.Int64Ptr(75),
				CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
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

func Test_mergeSLOSpecResourceQoSStrategy(t *testing.T) {
	testingDefaultSpec := util.DefaultResourceQoSStrategy()

	testingNewSpec := testingDefaultSpec.DeepCopy()
	testingNewSpec.BE.MemoryQoS.WmarkRatio = pointer.Int64Ptr(0)

	testingNewSpec1 := &slov1alpha1.ResourceQoSStrategy{
		BE: &slov1alpha1.ResourceQoS{
			MemoryQoS: &slov1alpha1.MemoryQoSCfg{
				Enable: pointer.BoolPtr(true),
				MemoryQoS: slov1alpha1.MemoryQoS{
					WmarkRatio: pointer.Int64Ptr(90),
				},
			},
		},
	}

	testingMergedSpec := testingDefaultSpec.DeepCopy()
	testingMergedSpec.BE.MemoryQoS.Enable = pointer.BoolPtr(true)
	testingMergedSpec.BE.MemoryQoS.WmarkRatio = pointer.Int64Ptr(90)

	type args struct {
		defaultSpec *slov1alpha1.ResourceQoSStrategy
		newSpec     *slov1alpha1.ResourceQoSStrategy
	}
	tests := []struct {
		name string
		args args
		want *slov1alpha1.ResourceQoSStrategy
	}{
		{
			name: "both empty",
			args: args{
				defaultSpec: &slov1alpha1.ResourceQoSStrategy{},
				newSpec:     &slov1alpha1.ResourceQoSStrategy{},
			},
			want: &slov1alpha1.ResourceQoSStrategy{},
		},
		{
			name: "totally use new",
			args: args{
				defaultSpec: &slov1alpha1.ResourceQoSStrategy{},
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
			got := mergeSLOSpecResourceQoSStrategy(tt.args.defaultSpec, tt.args.newSpec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_mergeNoneResourceQoSIfDisabled(t *testing.T) {
	testDefault := util.DefaultResourceQoSStrategy()
	testAllNone := util.NoneResourceQoSStrategy()

	testLSMemQOSEnabled := testDefault.DeepCopy()
	testLSMemQOSEnabled.LS.MemoryQoS.Enable = pointer.BoolPtr(true)
	testLSMemQOSEnabledResult := util.NoneResourceQoSStrategy()
	testLSMemQOSEnabledResult.LS.MemoryQoS.Enable = pointer.BoolPtr(true)
	testLSMemQOSEnabledResult.LS.MemoryQoS.MemoryQoS = *util.DefaultMemoryQoS(apiext.QoSLS)

	type args struct {
		nodeCfg *slov1alpha1.NodeSLO
	}
	tests := []struct {
		name string
		args args
		want *slov1alpha1.ResourceQoSStrategy
	}{
		{
			name: "all disabled",
			args: args{
				nodeCfg: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceQoSStrategy: testDefault,
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
						ResourceQoSStrategy: testLSMemQOSEnabled,
					},
				},
			},
			want: testLSMemQOSEnabledResult,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeNoneResourceQoSIfDisabled(tt.args.nodeCfg.Spec.ResourceQoSStrategy)
			assert.Equal(t, tt.want, tt.args.nodeCfg.Spec.ResourceQoSStrategy)
		})
	}
}
