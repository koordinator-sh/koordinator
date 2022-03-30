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
	"k8s.io/component-base/featuregate"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func Test_mergeSLOSpecResourceUsedThresholdWithBE(t *testing.T) {
	testingDefaultSpec := util.DefaultNodeSLOSpecConfig().ResourceUsedThresholdWithBE.DeepCopy()
	testingNewSpec := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      util.BoolPtr(true),
		CPUSuppressThresholdPercent: util.Int64Ptr(80),
	}
	testingNewSpec1 := &slov1alpha1.ResourceThresholdStrategy{
		CPUSuppressThresholdPercent: util.Int64Ptr(80),
	}
	testingMergedSpec := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      util.BoolPtr(true),
		CPUSuppressThresholdPercent: util.Int64Ptr(80),
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
				Enable:                      util.BoolPtr(true),
				CPUSuppressThresholdPercent: util.Int64Ptr(80),
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

func Test_mergeDefaultNodeSLO(t *testing.T) {
	testingCustomNodeSLOSpec := slov1alpha1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
			CPUSuppressThresholdPercent: util.Int64Ptr(80),
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
							CPUSuppressThresholdPercent: util.Int64Ptr(100),
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
			r := resmanager{nodeSLO: tt.field.nodeSLO}
			r.mergeDefaultNodeSLO(tt.args.nodeSLO)
			assert.Equal(t, tt.want, r.nodeSLO)
		})
	}
}

func Test_createNodeSLO(t *testing.T) {
	testingNewNodeSLO := &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
				CPUSuppressThresholdPercent: util.Int64Ptr(80),
			},
		},
	}

	testingCreatedNodeSLO := &slov1alpha1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingCreatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = util.Int64Ptr(80)

	r := resmanager{nodeSLO: nil}

	r.createNodeSLO(testingNewNodeSLO)
	assert.Equal(t, testingCreatedNodeSLO, r.nodeSLO)
}

func Test_updateNodeSLOSpec(t *testing.T) {
	testingNewNodeSLO := &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
				CPUSuppressThresholdPercent: util.Int64Ptr(80),
			},
		},
	}
	testingUpdatedNodeSLO := &slov1alpha1.NodeSLO{
		Spec: util.DefaultNodeSLOSpecConfig(),
	}
	testingUpdatedNodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent = util.Int64Ptr(80)

	r := resmanager{
		nodeSLO: &slov1alpha1.NodeSLO{
			Spec: slov1alpha1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: util.Int64Ptr(90),
				},
			},
		},
	}

	r.updateNodeSLOSpec(testingNewNodeSLO)
	assert.Equal(t, testingUpdatedNodeSLO, r.nodeSLO)
}

func Test_isFeatureDisabled(t *testing.T) {
	type args struct {
		nodeSLO *slov1alpha1.NodeSLO
		feature featuregate.Feature
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			name:    "throw an error for nil nodeSLO",
			wantErr: true,
		},
		{
			name: "throw an error for config field is nil",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{},
				feature: features.BECPUSuppress,
			},
			wantErr: true,
		},
		{
			name: "throw an error for config field is nil",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
							Enable: util.BoolPtr(false),
						},
					},
				},
				feature: featuregate.Feature("unknown_feature"),
			},
			wantErr: true,
		},
		{
			name: "use default config for nil switch",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{},
					},
				},
				feature: features.BECPUSuppress,
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "parse config successfully",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
							Enable: util.BoolPtr(false),
						},
					},
				},
				feature: features.BECPUSuppress,
			},
			want:    true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := isFeatureDisabled(tt.args.nodeSLO, tt.args.feature)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}
