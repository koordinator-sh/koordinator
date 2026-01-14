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

package defragmentation

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
)

const (
	Name = "GPUDefragmentation"
)

// Defragmentation is the GPU defragmentation plugin
type Defragmentation struct {
	handle framework.Handle
	args   *config.DefragmentationArgs

	// migrationManager manages pod migrations
	migrationManager *MigrationManager

	// safetyChecker checks if pods can be safely migrated
	safetyChecker *SafetyChecker

	// metricsCollector collects metrics
	metricsCollector *MetricsCollector
}

var _ framework.DeschedulePlugin = &Defragmentation{}

// New creates a new Defragmentation plugin
func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	defragArgs, ok := args.(*config.DefragmentationArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type DefragmentationArgs, got %T", args)
	}

	safetyChecker := NewSafetyChecker(handle.ClientSet(), &defragArgs.SafetyConfig)
	migrationManager := NewMigrationManager(handle.ClientSet(), &defragArgs.SafetyConfig)
	metricsCollector := NewMetricsCollector()

	return &Defragmentation{
		handle:           handle,
		args:             defragArgs,
		migrationManager: migrationManager,
		safetyChecker:    safetyChecker,
		metricsCollector: metricsCollector,
	}, nil
}

// Name returns the plugin name
func (d *Defragmentation) Name() string {
	return Name
}

// Deschedule executes defragmentation
func (d *Defragmentation) Deschedule(
	ctx context.Context,
	nodes []*corev1.Node,
) *framework.Status {

	// 1. Check if defragmentation should run
	if !d.shouldRunDefragmentation(ctx) {
		klog.V(4).InfoS("Skipping defragmentation", "reason", "not in execution window")
		return nil
	}

	// 2. Evaluate cluster fragmentation state
	fragmentationReport := d.evaluateClusterFragmentation(ctx, nodes)

	klog.V(3).InfoS("Cluster fragmentation report",
		"averageFragmentation", fragmentationReport.AverageFragmentation,
		"highlyFragmentedNodes", len(fragmentationReport.HighlyFragmentedNodes),
		"totalNodes", len(nodes),
	)

	// 3. Check if defragmentation is needed
	if fragmentationReport.AverageFragmentation < d.args.FragmentationThreshold {
		klog.V(4).InfoS("Fragmentation below threshold, skipping",
			"current", fragmentationReport.AverageFragmentation,
			"threshold", d.args.FragmentationThreshold,
		)
		return nil
	}

	// 4. Generate defragmentation plan
	plan := d.generateDefragmentationPlan(ctx, fragmentationReport, nodes)
	if plan == nil || len(plan.Migrations) == 0 {
		klog.V(4).InfoS("No defragmentation plan generated")
		return nil
	}

	klog.V(3).InfoS("Generated defragmentation plan",
		"totalMigrations", len(plan.Migrations),
		"expectedImprovement", plan.ExpectedImprovement,
	)

	// 5. Execute defragmentation (if not dry-run mode)
	if !d.args.SafetyConfig.DryRun {
		if err := d.executePlan(ctx, plan); err != nil {
			klog.ErrorS(err, "Failed to execute defragmentation plan")
			return &framework.Status{Err: err}
		}
	} else {
		klog.V(3).InfoS("Dry run mode, plan not executed", "plan", plan)
	}

	// 6. Record metrics
	d.metricsCollector.RecordDefragmentation(plan, fragmentationReport)

	return nil
}

// shouldRunDefragmentation checks if defragmentation should run
func (d *Defragmentation) shouldRunDefragmentation(ctx context.Context) bool {
	// Check if enabled
	if !d.args.Enabled {
		return false
	}

	// Check if in low peak period
	if !d.isInLowPeakPeriod() {
		return false
	}

	// Check cluster load
	clusterLoad := d.getClusterLoad(ctx)
	if clusterLoad > d.args.SafetyConfig.MaxClusterLoadThreshold {
		klog.V(4).InfoS("Cluster load too high",
			"current", clusterLoad,
			"threshold", d.args.SafetyConfig.MaxClusterLoadThreshold,
		)
		return false
	}

	// Check ongoing migrations
	ongoingMigrations := d.migrationManager.CountOngoingMigrations()
	if ongoingMigrations >= d.args.SafetyConfig.MaxConcurrentMigrations {
		klog.V(4).InfoS("Too many ongoing migrations",
			"current", ongoingMigrations,
			"max", d.args.SafetyConfig.MaxConcurrentMigrations,
		)
		return false
	}

	return true
}

// isInLowPeakPeriod checks if current time is in low peak period
func (d *Defragmentation) isInLowPeakPeriod() bool {
	if len(d.args.LowPeakPeriods) == 0 {
		return true // If not configured, always allow
	}

	now := time.Now()
	currentDay := now.Weekday().String()

	for _, period := range d.args.LowPeakPeriods {
		// Check day of week
		dayMatch := false
		for _, day := range period.Days {
			if day == currentDay {
				dayMatch = true
				break
			}
		}

		if !dayMatch {
			continue
		}

		// Parse times
		startTime, err := time.Parse("15:04", period.StartTime)
		if err != nil {
			klog.ErrorS(err, "Failed to parse start time", "time", period.StartTime)
			continue
		}

		endTime, err := time.Parse("15:04", period.EndTime)
		if err != nil {
			klog.ErrorS(err, "Failed to parse end time", "time", period.EndTime)
			continue
		}

		// Construct today's time points
		currentTime := time.Date(now.Year(), now.Month(), now.Day(),
			now.Hour(), now.Minute(), 0, 0, now.Location())
		todayStart := time.Date(now.Year(), now.Month(), now.Day(),
			startTime.Hour(), startTime.Minute(), 0, 0, now.Location())
		todayEnd := time.Date(now.Year(), now.Month(), now.Day(),
			endTime.Hour(), endTime.Minute(), 0, 0, now.Location())

		// Handle cross-day periods
		if todayEnd.Before(todayStart) {
			todayEnd = todayEnd.Add(24 * time.Hour)
		}

		// Check if in time range
		if currentTime.After(todayStart) && currentTime.Before(todayEnd) {
			return true
		}
	}

	return false
}

// getClusterLoad gets the current cluster load
func (d *Defragmentation) getClusterLoad(ctx context.Context) float64 {
	// TODO: Implement cluster load calculation
	// This should integrate with NodeMetrics or other monitoring systems
	return 0.0
}

// FragmentationReport contains fragmentation information
type FragmentationReport struct {
	// AverageFragmentation is the average fragmentation rate
	AverageFragmentation float64

	// HighlyFragmentedNodes are nodes with high fragmentation
	HighlyFragmentedNodes []*NodeFragmentationInfo

	// NodeFragmentations maps node name to fragmentation info
	NodeFragmentations map[string]*NodeFragmentationInfo

	// TotalGPUs is the total number of GPUs
	TotalGPUs int

	// FragmentedGPUs is the number of fragmented GPUs
	FragmentedGPUs int
}

// NodeFragmentationInfo contains fragmentation info for a node
type NodeFragmentationInfo struct {
	NodeName string

	// FragmentationRate is the fragmentation rate (0-100)
	FragmentationRate float64

	// Devices is the list of GPU devices
	Devices []*GPUDeviceInfo

	// MigratablePods is the list of pods that can be migrated
	MigratablePods []*corev1.Pod
}

// GPUDeviceInfo contains GPU device information
type GPUDeviceInfo struct {
	Minor int

	// Total resources
	TotalMemory int64
	TotalCore   int64

	// Used resources
	UsedMemory int64
	UsedCore   int64

	// Utilization
	Utilization float64

	// IsFragmented indicates if this GPU is fragmented
	IsFragmented bool

	// Pods using this GPU
	Pods []*corev1.Pod
}

// evaluateClusterFragmentation evaluates cluster fragmentation
func (d *Defragmentation) evaluateClusterFragmentation(
	ctx context.Context,
	nodes []*corev1.Node,
) *FragmentationReport {

	report := &FragmentationReport{
		NodeFragmentations:    make(map[string]*NodeFragmentationInfo),
		HighlyFragmentedNodes: make([]*NodeFragmentationInfo, 0),
	}

	totalFragmentation := 0.0
	nodeCount := 0

	for _, node := range nodes {
		nodeInfo := d.evaluateNodeFragmentation(ctx, node)
		if nodeInfo == nil {
			continue
		}

		report.NodeFragmentations[node.Name] = nodeInfo
		report.TotalGPUs += len(nodeInfo.Devices)

		for _, device := range nodeInfo.Devices {
			if device.IsFragmented {
				report.FragmentedGPUs++
			}
		}

		totalFragmentation += nodeInfo.FragmentationRate
		nodeCount++

		// Mark highly fragmented nodes
		if nodeInfo.FragmentationRate > d.args.FragmentationThreshold {
			report.HighlyFragmentedNodes = append(report.HighlyFragmentedNodes, nodeInfo)
		}
	}

	if nodeCount > 0 {
		report.AverageFragmentation = totalFragmentation / float64(nodeCount)
	}

	// Sort by fragmentation rate
	sort.Slice(report.HighlyFragmentedNodes, func(i, j int) bool {
		return report.HighlyFragmentedNodes[i].FragmentationRate >
			report.HighlyFragmentedNodes[j].FragmentationRate
	})

	return report
}

// evaluateNodeFragmentation evaluates fragmentation for a single node
func (d *Defragmentation) evaluateNodeFragmentation(
	ctx context.Context,
	node *corev1.Node,
) *NodeFragmentationInfo {

	// TODO: Get device info from Device CRD
	// This is a placeholder implementation
	return nil
}

// calculateUtilizationVariance calculates utilization variance
func (d *Defragmentation) calculateUtilizationVariance(devices []*GPUDeviceInfo) float64 {
	if len(devices) == 0 {
		return 0
	}

	var sum, sumSq float64
	for _, device := range devices {
		sum += device.Utilization
		sumSq += device.Utilization * device.Utilization
	}

	mean := sum / float64(len(devices))
	variance := (sumSq / float64(len(devices))) - (mean * mean)

	return math.Sqrt(variance)
}

// DefragmentationPlan contains the defragmentation plan
type DefragmentationPlan struct {
	// Migrations is the list of migration tasks
	Migrations []*MigrationTask

	// ExpectedImprovement is the expected improvement
	ExpectedImprovement float64

	// GeneratedAt is when the plan was generated
	GeneratedAt time.Time
}

// MigrationTask represents a pod migration task
type MigrationTask struct {
	// Pod is the pod to migrate
	Pod *corev1.Pod

	// SourceNode is the source node
	SourceNode string

	// TargetNode is the target node
	TargetNode string

	// Reason is the migration reason
	Reason string

	// Priority is the migration priority
	Priority int

	// ExpectedBenefit is the expected benefit
	ExpectedBenefit float64
}

// generateDefragmentationPlan generates a defragmentation plan
func (d *Defragmentation) generateDefragmentationPlan(
	ctx context.Context,
	report *FragmentationReport,
	nodes []*corev1.Node,
) *DefragmentationPlan {

	plan := &DefragmentationPlan{
		Migrations:  make([]*MigrationTask, 0),
		GeneratedAt: time.Now(),
	}

	// Generate plan based on strategy
	switch d.args.TargetConfig.Strategy {
	case config.DefragmentationStrategyCompact:
		plan = d.generateCompactPlan(ctx, report, nodes)
	case config.DefragmentationStrategyBalance:
		plan = d.generateBalancePlan(ctx, report, nodes)
	case config.DefragmentationStrategyHybrid:
		plan = d.generateHybridPlan(ctx, report, nodes)
	default:
		plan = d.generateCompactPlan(ctx, report, nodes)
	}

	// Limit migration count
	if len(plan.Migrations) > d.args.SafetyConfig.MaxMigrationsPerCycle {
		plan.Migrations = plan.Migrations[:d.args.SafetyConfig.MaxMigrationsPerCycle]
	}

	// Calculate expected improvement
	plan.ExpectedImprovement = d.calculateExpectedImprovement(plan, report)

	return plan
}

// generateCompactPlan generates a compact strategy plan
func (d *Defragmentation) generateCompactPlan(
	ctx context.Context,
	report *FragmentationReport,
	nodes []*corev1.Node,
) *DefragmentationPlan {

	plan := &DefragmentationPlan{
		Migrations: make([]*MigrationTask, 0),
	}

	// Sort nodes by fragmentation rate (descending)
	sortedNodes := make([]*NodeFragmentationInfo, 0)
	for _, nodeInfo := range report.NodeFragmentations {
		sortedNodes = append(sortedNodes, nodeInfo)
	}

	sort.Slice(sortedNodes, func(i, j int) bool {
		return sortedNodes[i].FragmentationRate > sortedNodes[j].FragmentationRate
	})

	// Migrate pods from most fragmented nodes
	for _, sourceNodeInfo := range sortedNodes {
		if len(plan.Migrations) >= d.args.SafetyConfig.MaxMigrationsPerCycle {
			break
		}

		// Select migratable pods
		for _, pod := range sourceNodeInfo.MigratablePods {
			if len(plan.Migrations) >= d.args.SafetyConfig.MaxMigrationsPerCycle {
				break
			}

			// Find best target node
			targetNode := d.findBestTargetNode(ctx, pod, sourceNodeInfo.NodeName, nodes, report)
			if targetNode == "" {
				continue
			}

			// Create migration task
			task := &MigrationTask{
				Pod:             pod,
				SourceNode:      sourceNodeInfo.NodeName,
				TargetNode:      targetNode,
				Reason:          "compact-fragmented-resources",
				Priority:        d.calculateMigrationPriority(pod, sourceNodeInfo),
				ExpectedBenefit: d.calculateMigrationBenefit(pod, sourceNodeInfo.NodeName, targetNode, report),
			}

			plan.Migrations = append(plan.Migrations, task)
		}
	}

	// Sort by priority
	sort.Slice(plan.Migrations, func(i, j int) bool {
		return plan.Migrations[i].Priority > plan.Migrations[j].Priority
	})

	return plan
}

// generateBalancePlan generates a balance strategy plan
func (d *Defragmentation) generateBalancePlan(
	ctx context.Context,
	report *FragmentationReport,
	nodes []*corev1.Node,
) *DefragmentationPlan {

	plan := &DefragmentationPlan{
		Migrations: make([]*MigrationTask, 0),
	}

	// Calculate target rate
	targetRate := d.args.TargetConfig.TargetFragmentationRate
	if targetRate == 0 {
		targetRate = 20.0 // Default target 20%
	}

	// Find nodes above target
	for _, nodeInfo := range report.NodeFragmentations {
		if nodeInfo.FragmentationRate <= targetRate {
			continue
		}

		// TODO: Select pods for balancing
		// This is a placeholder
	}

	return plan
}

// generateHybridPlan generates a hybrid strategy plan
func (d *Defragmentation) generateHybridPlan(
	ctx context.Context,
	report *FragmentationReport,
	nodes []*corev1.Node,
) *DefragmentationPlan {

	// Hybrid: use compact for highly fragmented nodes, balance for others
	compactPlan := d.generateCompactPlan(ctx, report, nodes)
	balancePlan := d.generateBalancePlan(ctx, report, nodes)

	// Merge plans, deduplicate
	mergedMigrations := make(map[string]*MigrationTask)

	for _, task := range compactPlan.Migrations {
		key := fmt.Sprintf("%s/%s", task.Pod.Namespace, task.Pod.Name)
		mergedMigrations[key] = task
	}

	for _, task := range balancePlan.Migrations {
		key := fmt.Sprintf("%s/%s", task.Pod.Namespace, task.Pod.Name)
		if _, exists := mergedMigrations[key]; !exists {
			mergedMigrations[key] = task
		}
	}

	// Convert to list and sort
	migrations := make([]*MigrationTask, 0, len(mergedMigrations))
	for _, task := range mergedMigrations {
		migrations = append(migrations, task)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].ExpectedBenefit > migrations[j].ExpectedBenefit
	})

	return &DefragmentationPlan{
		Migrations:  migrations,
		GeneratedAt: time.Now(),
	}
}

// findBestTargetNode finds the best target node for migration
func (d *Defragmentation) findBestTargetNode(
	ctx context.Context,
	pod *corev1.Pod,
	sourceNode string,
	nodes []*corev1.Node,
	report *FragmentationReport,
) string {
	// TODO: Implement target node selection logic
	return ""
}

// calculateMigrationPriority calculates migration priority
func (d *Defragmentation) calculateMigrationPriority(
	pod *corev1.Pod,
	nodeInfo *NodeFragmentationInfo,
) int {
	// Higher fragmentation = higher priority
	return int(nodeInfo.FragmentationRate)
}

// calculateMigrationBenefit calculates migration benefit
func (d *Defragmentation) calculateMigrationBenefit(
	pod *corev1.Pod,
	sourceNode string,
	targetNode string,
	report *FragmentationReport,
) float64 {
	// TODO: Implement benefit calculation
	return 0.0
}

// calculateExpectedImprovement calculates expected improvement
func (d *Defragmentation) calculateExpectedImprovement(
	plan *DefragmentationPlan,
	report *FragmentationReport,
) float64 {
	// TODO: Implement improvement calculation
	return 0.0
}

// executePlan executes the defragmentation plan
func (d *Defragmentation) executePlan(ctx context.Context, plan *DefragmentationPlan) error {
	for _, task := range plan.Migrations {
		// Check if can migrate
		canMigrate, reason := d.safetyChecker.CanMigratePod(ctx, task.Pod)
		if !canMigrate {
			klog.V(4).InfoS("Cannot migrate pod",
				"pod", klog.KObj(task.Pod),
				"reason", reason,
			)
			continue
		}

		// Execute migration
		if err := d.migrationManager.ExecuteMigration(ctx, task); err != nil {
			klog.ErrorS(err, "Failed to execute migration",
				"pod", klog.KObj(task.Pod),
			)
			// Continue with other migrations
			continue
		}
	}

	return nil
}
