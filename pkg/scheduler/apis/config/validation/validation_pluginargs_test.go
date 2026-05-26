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

package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func TestValidateLoadAwareSchedulingArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    *config.LoadAwareSchedulingArgs
		wantErr bool
	}{
		{
			name:    "empty args",
			args:    &config.LoadAwareSchedulingArgs{},
			wantErr: false,
		},
		{
			name: "valid nodeMetricExpirationSeconds",
			args: &config.LoadAwareSchedulingArgs{
				NodeMetricExpirationSeconds: ptr.To[int64](180),
			},
			wantErr: false,
		},
		{
			name: "zero nodeMetricExpirationSeconds",
			args: &config.LoadAwareSchedulingArgs{
				NodeMetricExpirationSeconds: ptr.To[int64](0),
			},
			wantErr: true,
		},
		{
			name: "negative nodeMetricExpirationSeconds",
			args: &config.LoadAwareSchedulingArgs{
				NodeMetricExpirationSeconds: ptr.To[int64](-1),
			},
			wantErr: true,
		},
		{
			name: "valid resource weights",
			args: &config.LoadAwareSchedulingArgs{
				ResourceWeights: map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    1,
					corev1.ResourceMemory: 1,
				},
				EstimatedScalingFactors: map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    85,
					corev1.ResourceMemory: 70,
				},
			},
			wantErr: false,
		},
		{
			name: "resource weight zero",
			args: &config.LoadAwareSchedulingArgs{
				ResourceWeights: map[corev1.ResourceName]int64{
					corev1.ResourceCPU: 0,
				},
				EstimatedScalingFactors: map[corev1.ResourceName]int64{
					corev1.ResourceCPU: 85,
				},
			},
			wantErr: true,
		},
		{
			name: "resource weight exceeds 100",
			args: &config.LoadAwareSchedulingArgs{
				ResourceWeights: map[corev1.ResourceName]int64{
					corev1.ResourceCPU: 101,
				},
				EstimatedScalingFactors: map[corev1.ResourceName]int64{
					corev1.ResourceCPU: 85,
				},
			},
			wantErr: true,
		},
		{
			name: "dominantResourceWeight negative",
			args: &config.LoadAwareSchedulingArgs{
				DominantResourceWeight: -1,
			},
			wantErr: true,
		},
		{
			name: "dominantResourceWeight exceeds 100",
			args: &config.LoadAwareSchedulingArgs{
				DominantResourceWeight: 101,
			},
			wantErr: true,
		},
		{
			name: "valid dominantResourceWeight boundary",
			args: &config.LoadAwareSchedulingArgs{
				DominantResourceWeight: 100,
			},
			wantErr: false,
		},
		{
			name: "usageThreshold negative",
			args: &config.LoadAwareSchedulingArgs{
				UsageThresholds: map[corev1.ResourceName]int64{
					corev1.ResourceCPU: -1,
				},
			},
			wantErr: true,
		},
		{
			name: "usageThreshold exceeds 100",
			args: &config.LoadAwareSchedulingArgs{
				UsageThresholds: map[corev1.ResourceName]int64{
					corev1.ResourceCPU: 101,
				},
			},
			wantErr: true,
		},
		{
			name: "estimatedScalingFactor zero",
			args: &config.LoadAwareSchedulingArgs{
				EstimatedScalingFactors: map[corev1.ResourceName]int64{
					corev1.ResourceCPU: 0,
				},
			},
			wantErr: true,
		},
		{
			name: "estimatedScalingFactor exceeds 100",
			args: &config.LoadAwareSchedulingArgs{
				EstimatedScalingFactors: map[corev1.ResourceName]int64{
					corev1.ResourceCPU: 101,
				},
			},
			wantErr: true,
		},
		{
			name: "resourceWeight missing estimatedScalingFactor",
			args: &config.LoadAwareSchedulingArgs{
				ResourceWeights: map[corev1.ResourceName]int64{
					corev1.ResourceCPU: 1,
				},
				EstimatedScalingFactors: map[corev1.ResourceName]int64{},
			},
			wantErr: true,
		},
		{
			name: "valid aggregated args",
			args: &config.LoadAwareSchedulingArgs{
				Aggregated: &config.LoadAwareSchedulingAggregatedArgs{
					UsageAggregationType:    extension.P95,
					UsageAggregatedDuration: metav1.Duration{Duration: time.Minute},
					ScoreAggregationType:    extension.P90,
					ScoreAggregatedDuration: metav1.Duration{Duration: time.Minute},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid aggregation type",
			args: &config.LoadAwareSchedulingArgs{
				Aggregated: &config.LoadAwareSchedulingAggregatedArgs{
					UsageAggregationType: extension.AggregationType("invalid"),
				},
			},
			wantErr: true,
		},
		{
			name: "negative aggregated duration",
			args: &config.LoadAwareSchedulingArgs{
				Aggregated: &config.LoadAwareSchedulingAggregatedArgs{
					UsageAggregatedDuration: metav1.Duration{Duration: -time.Minute},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLoadAwareSchedulingArgs(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateElasticQuotaArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    *config.ElasticQuotaArgs
		wantErr bool
	}{
		{
			name:    "empty args",
			args:    &config.ElasticQuotaArgs{},
			wantErr: false,
		},
		{
			name: "valid defaultQuotaGroupMax",
			args: &config.ElasticQuotaArgs{
				DefaultQuotaGroupMax: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10"),
					corev1.ResourceMemory: resource.MustParse("20Gi"),
				},
			},
			wantErr: false,
		},
		{
			name: "negative defaultQuotaGroupMax",
			args: &config.ElasticQuotaArgs{
				DefaultQuotaGroupMax: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("-1"),
				},
			},
			wantErr: true,
		},
		{
			name: "negative systemQuotaGroupMax",
			args: &config.ElasticQuotaArgs{
				SystemQuotaGroupMax: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("-1Gi"),
				},
			},
			wantErr: true,
		},
		{
			name: "negative delayEvictTime",
			args: &config.ElasticQuotaArgs{
				DelayEvictTime: metav1.Duration{Duration: -time.Second},
			},
			wantErr: true,
		},
		{
			name: "negative revokePodInterval",
			args: &config.ElasticQuotaArgs{
				RevokePodInterval: metav1.Duration{Duration: -time.Second},
			},
			wantErr: true,
		},
		{
			name: "valid durations",
			args: &config.ElasticQuotaArgs{
				DelayEvictTime:    metav1.Duration{Duration: time.Minute},
				RevokePodInterval: metav1.Duration{Duration: time.Second},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateElasticQuotaArgs(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCoschedulingArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    *config.CoschedulingArgs
		wantErr bool
	}{
		{
			name: "valid args",
			args: &config.CoschedulingArgs{
				DefaultTimeout:    metav1.Duration{Duration: 10 * time.Minute},
				ControllerWorkers: 1,
			},
			wantErr: false,
		},
		{
			name: "negative defaultTimeout",
			args: &config.CoschedulingArgs{
				DefaultTimeout:    metav1.Duration{Duration: -time.Second},
				ControllerWorkers: 1,
			},
			wantErr: true,
		},
		{
			name: "controllerWorkers zero",
			args: &config.CoschedulingArgs{
				ControllerWorkers: 0,
			},
			wantErr: true,
		},
		{
			name: "controllerWorkers negative",
			args: &config.CoschedulingArgs{
				ControllerWorkers: -1,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCoschedulingArgs(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateDeviceShareArgs(t *testing.T) {
	path := field.NewPath("deviceShareArgs")
	tests := []struct {
		name    string
		args    *config.DeviceShareArgs
		wantErr bool
	}{
		{
			name:    "nil scoring strategy",
			args:    &config.DeviceShareArgs{},
			wantErr: false,
		},
		{
			name: "valid scoring strategy",
			args: &config.DeviceShareArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Resources: []schedconfig.ResourceSpec{
						{Name: string(corev1.ResourceCPU), Weight: 1},
						{Name: string(corev1.ResourceMemory), Weight: 100},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "resource weight zero",
			args: &config.DeviceShareArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Resources: []schedconfig.ResourceSpec{
						{Name: string(corev1.ResourceCPU), Weight: 0},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "resource weight exceeds 100",
			args: &config.DeviceShareArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Resources: []schedconfig.ResourceSpec{
						{Name: string(corev1.ResourceCPU), Weight: 101},
					},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDeviceShareArgs(path, tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateReservationArgs(t *testing.T) {
	path := field.NewPath("reservationArgs")
	tests := []struct {
		name    string
		args    *config.ReservationArgs
		wantErr bool
	}{
		{
			name: "valid defaults",
			args: &config.ReservationArgs{
				MinCandidateNodesPercentage: 10,
				MinCandidateNodesAbsolute:   100,
				GCDurationSeconds:           86400,
				GCIntervalSeconds:           60,
				ResyncIntervalSeconds:       60,
			},
			wantErr: false,
		},
		{
			name: "zero values are valid",
			args: &config.ReservationArgs{
				MinCandidateNodesPercentage: 0,
				MinCandidateNodesAbsolute:   0,
				GCDurationSeconds:           0,
				GCIntervalSeconds:           0,
				ResyncIntervalSeconds:       0,
			},
			wantErr: false,
		},
		{
			name: "minCandidateNodesPercentage negative",
			args: &config.ReservationArgs{
				MinCandidateNodesPercentage: -1,
			},
			wantErr: true,
		},
		{
			name: "minCandidateNodesPercentage exceeds 100",
			args: &config.ReservationArgs{
				MinCandidateNodesPercentage: 101,
			},
			wantErr: true,
		},
		{
			name: "minCandidateNodesAbsolute negative",
			args: &config.ReservationArgs{
				MinCandidateNodesAbsolute: -1,
			},
			wantErr: true,
		},
		{
			name: "gcDurationSeconds negative",
			args: &config.ReservationArgs{
				GCDurationSeconds: -1,
			},
			wantErr: true,
		},
		{
			name: "gcIntervalSeconds negative",
			args: &config.ReservationArgs{
				GCIntervalSeconds: -1,
			},
			wantErr: true,
		},
		{
			name: "resyncIntervalSeconds negative",
			args: &config.ReservationArgs{
				ResyncIntervalSeconds: -1,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateReservationArgs(path, tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateNodeNUMAResourceArgs(t *testing.T) {
	path := field.NewPath("nodeNUMAResourceArgs")
	validStrategy := &config.ScoringStrategy{
		Resources: []schedconfig.ResourceSpec{
			{Name: string(corev1.ResourceCPU), Weight: 1},
		},
	}
	tests := []struct {
		name    string
		args    *config.NodeNUMAResourceArgs
		wantErr bool
	}{
		{
			name: "valid args with empty bind policy",
			args: &config.NodeNUMAResourceArgs{
				DefaultCPUBindPolicy: "",
				ScoringStrategy:      validStrategy,
				NUMAScoringStrategy:  validStrategy,
			},
			wantErr: false,
		},
		{
			name: "valid FullPCPUs bind policy",
			args: &config.NodeNUMAResourceArgs{
				DefaultCPUBindPolicy: config.CPUBindPolicyFullPCPUs,
				ScoringStrategy:      validStrategy,
				NUMAScoringStrategy:  validStrategy,
			},
			wantErr: false,
		},
		{
			name: "valid SpreadByPCPUs bind policy",
			args: &config.NodeNUMAResourceArgs{
				DefaultCPUBindPolicy: config.CPUBindPolicySpreadByPCPUs,
				ScoringStrategy:      validStrategy,
				NUMAScoringStrategy:  validStrategy,
			},
			wantErr: false,
		},
		{
			name: "invalid CPU bind policy",
			args: &config.NodeNUMAResourceArgs{
				DefaultCPUBindPolicy: "InvalidPolicy",
				ScoringStrategy:      validStrategy,
				NUMAScoringStrategy:  validStrategy,
			},
			wantErr: true,
		},
		{
			name: "nil ScoringStrategy",
			args: &config.NodeNUMAResourceArgs{
				ScoringStrategy:     nil,
				NUMAScoringStrategy: validStrategy,
			},
			wantErr: true,
		},
		{
			name: "nil NUMAScoringStrategy",
			args: &config.NodeNUMAResourceArgs{
				ScoringStrategy:     validStrategy,
				NUMAScoringStrategy: nil,
			},
			wantErr: true,
		},
		{
			name: "invalid resource weight in ScoringStrategy",
			args: &config.NodeNUMAResourceArgs{
				ScoringStrategy: &config.ScoringStrategy{
					Resources: []schedconfig.ResourceSpec{
						{Name: string(corev1.ResourceCPU), Weight: 0},
					},
				},
				NUMAScoringStrategy: validStrategy,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNodeNUMAResourceArgs(path, tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSchedulingHintArgs(t *testing.T) {
	path := field.NewPath("schedulingHintArgs")
	tests := []struct {
		name    string
		args    *config.SchedulingHintArgs
		wantErr bool
	}{
		{
			name: "valid maxHintNodes",
			args: &config.SchedulingHintArgs{
				MaxHintNodes: 100,
			},
			wantErr: false,
		},
		{
			name: "maxHintNodes one",
			args: &config.SchedulingHintArgs{
				MaxHintNodes: 1,
			},
			wantErr: false,
		},
		{
			name: "maxHintNodes zero",
			args: &config.SchedulingHintArgs{
				MaxHintNodes: 0,
			},
			wantErr: true,
		},
		{
			name: "maxHintNodes negative",
			args: &config.SchedulingHintArgs{
				MaxHintNodes: -1,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSchedulingHintArgs(path, tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
