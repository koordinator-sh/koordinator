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

package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// DefragmentationArgs holds arguments for GPU defragmentation plugin
type DefragmentationArgs struct {
	metav1.TypeMeta

	// Enabled indicates whether defragmentation is enabled
	Enabled bool `json:"enabled,omitempty"`

	// Schedule is the cron expression for scheduling defragmentation
	Schedule string `json:"schedule,omitempty"`

	// LowPeakPeriods defines time periods when defragmentation can run
	LowPeakPeriods []TimePeriod `json:"lowPeakPeriods,omitempty"`

	// FragmentationThreshold is the threshold to trigger defragmentation (0-100)
	FragmentationThreshold float64 `json:"fragmentationThreshold,omitempty"`

	// SafetyConfig defines safety constraints for defragmentation
	SafetyConfig DefragmentationSafetyConfig `json:"safetyConfig,omitempty"`

	// TargetConfig defines target configuration for defragmentation
	TargetConfig DefragmentationTargetConfig `json:"targetConfig,omitempty"`
}

// TimePeriod defines a time period
type TimePeriod struct {
	// StartTime is the start time in HH:MM format
	StartTime string `json:"startTime"`

	// EndTime is the end time in HH:MM format
	EndTime string `json:"endTime"`

	// Days is the list of days (Monday, Tuesday, etc.)
	Days []string `json:"days"`

	// Timezone is the timezone
	Timezone string `json:"timezone,omitempty"`
}

// DefragmentationSafetyConfig defines safety constraints
type DefragmentationSafetyConfig struct {
	// MaxMigrationsPerCycle is the maximum number of migrations per cycle
	MaxMigrationsPerCycle int `json:"maxMigrationsPerCycle,omitempty"`

	// MaxConcurrentMigrations is the maximum number of concurrent migrations
	MaxConcurrentMigrations int `json:"maxConcurrentMigrations,omitempty"`

	// RespectPDB indicates whether to respect PodDisruptionBudget
	RespectPDB bool `json:"respectPDB,omitempty"`

	// WhitelistNamespaces is the list of namespaces to defragment
	WhitelistNamespaces []string `json:"whitelistNamespaces,omitempty"`

	// BlacklistLabels is the map of labels to exclude from defragmentation
	BlacklistLabels map[string]string `json:"blacklistLabels,omitempty"`

	// BlacklistAnnotations is the map of annotations to exclude from defragmentation
	BlacklistAnnotations map[string]string `json:"blacklistAnnotations,omitempty"`

	// RequireGracefulShutdown indicates whether graceful shutdown is required
	RequireGracefulShutdown bool `json:"requireGracefulShutdown,omitempty"`

	// MinGracefulShutdownSeconds is the minimum graceful shutdown time in seconds
	MinGracefulShutdownSeconds int64 `json:"minGracefulShutdownSeconds,omitempty"`

	// MigrationTimeout is the timeout for migration
	MigrationTimeout metav1.Duration `json:"migrationTimeout,omitempty"`

	// MaxClusterLoadThreshold is the maximum cluster load threshold (0-100)
	MaxClusterLoadThreshold float64 `json:"maxClusterLoadThreshold,omitempty"`

	// MaxNodeLoadThreshold is the maximum node load threshold (0-100)
	MaxNodeLoadThreshold float64 `json:"maxNodeLoadThreshold,omitempty"`

	// MaxRetries is the maximum number of retries for failed migrations
	MaxRetries int `json:"maxRetries,omitempty"`

	// RetryInterval is the interval between retries
	RetryInterval metav1.Duration `json:"retryInterval,omitempty"`

	// DryRun indicates whether to run in dry-run mode
	DryRun bool `json:"dryRun,omitempty"`
}

// DefragmentationTargetConfig defines target configuration
type DefragmentationTargetConfig struct {
	// TargetFragmentationRate is the target fragmentation rate (0-100)
	TargetFragmentationRate float64 `json:"targetFragmentationRate,omitempty"`

	// PriorityNodeSelector selects nodes to prioritize for defragmentation
	PriorityNodeSelector map[string]string `json:"priorityNodeSelector,omitempty"`

	// Strategy is the defragmentation strategy
	Strategy DefragmentationStrategy `json:"strategy,omitempty"`
}

// DefragmentationStrategy defines the defragmentation strategy
type DefragmentationStrategy string

const (
	// DefragmentationStrategyCompact compacts pods to fewer nodes
	DefragmentationStrategyCompact DefragmentationStrategy = "Compact"

	// DefragmentationStrategyBalance balances fragmentation across nodes
	DefragmentationStrategyBalance DefragmentationStrategy = "Balance"

	// DefragmentationStrategyHybrid uses a hybrid approach
	DefragmentationStrategyHybrid DefragmentationStrategy = "Hybrid"
)

// DeepCopy methods for runtime.Object interface

func (in *DefragmentationArgs) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *DefragmentationArgs) DeepCopy() *DefragmentationArgs {
	if in == nil {
		return nil
	}
	out := new(DefragmentationArgs)
	in.DeepCopyInto(out)
	return out
}

func (in *DefragmentationArgs) DeepCopyInto(out *DefragmentationArgs) {
	*out = *in
	out.TypeMeta = in.TypeMeta

	if in.LowPeakPeriods != nil {
		in, out := &in.LowPeakPeriods, &out.LowPeakPeriods
		*out = make([]TimePeriod, len(*in))
		copy(*out, *in)
	}

	in.SafetyConfig.DeepCopyInto(&out.SafetyConfig)
	in.TargetConfig.DeepCopyInto(&out.TargetConfig)
}

func (in *DefragmentationSafetyConfig) DeepCopyInto(out *DefragmentationSafetyConfig) {
	*out = *in

	if in.WhitelistNamespaces != nil {
		in, out := &in.WhitelistNamespaces, &out.WhitelistNamespaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}

	if in.BlacklistLabels != nil {
		in, out := &in.BlacklistLabels, &out.BlacklistLabels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}

	if in.BlacklistAnnotations != nil {
		in, out := &in.BlacklistAnnotations, &out.BlacklistAnnotations
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}

	out.MigrationTimeout = in.MigrationTimeout
	out.RetryInterval = in.RetryInterval
}

func (in *DefragmentationTargetConfig) DeepCopyInto(out *DefragmentationTargetConfig) {
	*out = *in

	if in.PriorityNodeSelector != nil {
		in, out := &in.PriorityNodeSelector, &out.PriorityNodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}
