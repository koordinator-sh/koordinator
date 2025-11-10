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

package features

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/component-base/featuregate"
	"k8s.io/utils/ptr"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

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
			want:    true,
			wantErr: true,
		},
		{
			name: "throw an error for config field is nil",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{},
				feature: BECPUSuppress,
			},
			want:    true,
			wantErr: true,
		},
		{
			name: "throw an error for unknown feature",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
							Enable: ptr.To[bool](false),
						},
					},
				},
				feature: featuregate.Feature("unknown_feature"),
			},
			want:    true,
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
				feature: BECPUSuppress,
			},
			want:    true,
			wantErr: true,
		},
		{
			name: "parse config successfully",
			args: args{
				nodeSLO: &slov1alpha1.NodeSLO{
					Spec: slov1alpha1.NodeSLOSpec{
						ResourceUsedThresholdWithBE: &slov1alpha1.ResourceThresholdStrategy{
							Enable: ptr.To[bool](false),
						},
					},
				},
				feature: BECPUSuppress,
			},
			want:    true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := IsFeatureDisabled(tt.args.nodeSLO, tt.args.feature)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}
