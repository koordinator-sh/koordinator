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

package resmanager

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

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
