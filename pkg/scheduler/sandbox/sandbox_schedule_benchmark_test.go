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

package sandbox

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
	"k8s.io/kubernetes/pkg/scheduler"
	schedulerconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	internalqueue "k8s.io/kubernetes/pkg/scheduler/backend/queue"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	plfeature "k8s.io/kubernetes/pkg/scheduler/framework/plugins/feature"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodeports"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"
)

const sandboxBenchmarkNodeCount = 2000

func BenchmarkSandboxScorePercentage(b *testing.B) {
	fivePercent := int32(5)
	kubernetesAdaptive := int32(0)
	allNodes := int32(100)

	testCases := []struct {
		name       string
		percentage *int32
	}{
		{name: "sandbox-5", percentage: &fivePercent},
		{name: "kubernetes-adaptive-34", percentage: &kubernetesAdaptive},
		{name: "all-100", percentage: &allNodes},
	}

	for _, tt := range testCases {
		b.Run(tt.name, func(b *testing.B) {
			b.Run("equivalence", func(b *testing.B) {
				benchmarkSandboxDecisions(b, tt.percentage, true)
			})
			b.Run("full-path", func(b *testing.B) {
				benchmarkSandboxDecisions(b, tt.percentage, false)
			})
		})
	}
}

func benchmarkSandboxDecisions(b *testing.B, percentage *int32, equivalence bool) {
	b.Helper()
	b.StopTimer()

	ctx := context.Background()
	s := newSandboxBenchmarkScheduling(b, ctx, percentage, sandboxBenchmarkNodeCount)
	fwk := s.sched.Profiles["koord-scheduler"]
	pod := makeSandboxPod("benchmark", "benchmark-hash")
	pod.Spec.Containers = []corev1.Container{{
		Name: "main",
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
		},
	}}

	var evaluatedNodes int64
	var scoredNodes int64
	var fullPaths int64
	logger := klog.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		state := framework.NewCycleState()
		if equivalence {
			result, err := s.decide(ctx, state, fwk, pod)
			if err != nil {
				b.Fatal(err)
			}
			evaluatedNodes += int64(result.EvaluatedNodes)
			if result.EvaluatedNodes > 1 {
				fullPaths++
				scoredNodes += int64(result.FeasibleNodes)
			}
			continue
		}

		snapshot, err := s.updateSnapshot(logger, fwk)
		if err != nil {
			b.Fatal(err)
		}
		preFilter := s.runSandboxPreFilter(ctx, state, fwk, pod)
		result, _, err := s.scheduleSandboxPod(ctx, state, fwk, pod, snapshot, preFilter)
		if err != nil {
			b.Fatal(err)
		}
		evaluatedNodes += int64(result.EvaluatedNodes)
		scoredNodes += int64(result.FeasibleNodes)
		fullPaths++
	}
	b.StopTimer()

	operations := float64(b.N)
	b.ReportMetric(float64(evaluatedNodes)/operations, "evaluated-nodes/op")
	b.ReportMetric(float64(scoredNodes)/operations, "scored-nodes/op")
	b.ReportMetric(float64(fullPaths)*100/operations, "full-path-pct")
}

func BenchmarkSandboxPhaseCost(b *testing.B) {
	fivePercent := int32(5)
	kubernetesAdaptive := int32(0)
	allNodes := int32(100)

	testCases := []struct {
		name       string
		percentage *int32
	}{
		{name: "sandbox-5", percentage: &fivePercent},
		{name: "kubernetes-adaptive-34", percentage: &kubernetesAdaptive},
		{name: "all-100", percentage: &allNodes},
	}

	for _, tt := range testCases {
		b.Run(tt.name, func(b *testing.B) {
			b.Run("prefilter-filter", func(b *testing.B) {
				benchmarkSandboxFilterPhase(b, tt.percentage)
			})
			b.Run("prescore-score-sort", func(b *testing.B) {
				benchmarkSandboxScorePhase(b, tt.percentage)
			})
		})
	}
}

func benchmarkSandboxFilterPhase(b *testing.B, percentage *int32) {
	b.Helper()
	b.StopTimer()

	ctx := context.Background()
	s := newSandboxBenchmarkScheduling(b, ctx, percentage, sandboxBenchmarkNodeCount)
	fwk := s.sched.Profiles["koord-scheduler"]
	pod := makeSandboxPod("benchmark", "benchmark-hash")
	pod.Spec.Containers = []corev1.Container{{
		Name: "main",
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
		},
	}}
	snapshot, err := s.updateSnapshot(klog.Background(), fwk)
	if err != nil {
		b.Fatal(err)
	}
	nodes, err := snapshot.NodeInfos().List()
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		state := framework.NewCycleState()
		_, status, _ := fwk.RunPreFilterPlugins(ctx, state, pod)
		if !status.IsSuccess() {
			b.Fatal(status.AsError())
		}
		diagnosis := framework.Diagnosis{NodeToStatus: framework.NewDefaultNodeToStatus()}
		feasibleNodes, err := s.findNodesThatPassFilters(ctx, fwk, state, pod, &diagnosis, nodes)
		if err != nil {
			b.Fatal(err)
		}
		if len(feasibleNodes) != int(s.numFeasibleNodesToFind(percentage, sandboxBenchmarkNodeCount)) {
			b.Fatalf("unexpected feasible node count %d", len(feasibleNodes))
		}
	}
}

func benchmarkSandboxScorePhase(b *testing.B, percentage *int32) {
	b.Helper()
	b.StopTimer()

	ctx := context.Background()
	s := newSandboxBenchmarkScheduling(b, ctx, percentage, sandboxBenchmarkNodeCount)
	fwk := s.sched.Profiles["koord-scheduler"]
	pod := makeSandboxPod("benchmark", "benchmark-hash")
	pod.Spec.Containers = []corev1.Container{{
		Name: "main",
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
		},
	}}
	snapshot, err := s.updateSnapshot(klog.Background(), fwk)
	if err != nil {
		b.Fatal(err)
	}
	nodes, err := snapshot.NodeInfos().List()
	if err != nil {
		b.Fatal(err)
	}
	nodes = nodes[:s.numFeasibleNodesToFind(percentage, sandboxBenchmarkNodeCount)]

	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		state := framework.NewCycleState()
		nodeScores, err := prioritizeNodes(ctx, s.sched.Extenders, fwk, state, pod, nodes)
		if err != nil {
			b.Fatal(err)
		}
		sort.Slice(nodeScores, func(i, j int) bool {
			return nodeScores[i].TotalScore > nodeScores[j].TotalScore ||
				(nodeScores[i].TotalScore == nodeScores[j].TotalScore && nodeScores[i].Randomizer > nodeScores[j].Randomizer)
		})
	}
}

func newSandboxBenchmarkScheduling(b *testing.B, ctx context.Context, percentage *int32, nodeCount int) *equivalenceScheduling {
	b.Helper()
	logger, _ := ktesting.NewTestContext(b)
	metrics.Register()

	schedulerCache := cache.New(ctx, 30*time.Second, nil)
	for i := 0; i < nodeCount; i++ {
		node := makeNode(fmt.Sprintf("node-%04d", i), "128", "256Gi")
		schedulerCache.AddNode(logger, node)
	}
	snapshot := cache.NewEmptySnapshot()

	fitFactory := frameworkruntime.FactoryAdapter(plfeature.Features{}, noderesources.NewFit)
	nodePortsFactory := frameworkruntime.FactoryAdapter(plfeature.Features{}, nodeports.New)
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterPluginAsExtensions(noderesources.Name, fitFactory, "PreFilter", "Filter", "PreScore", "Score"),
		schedulertesting.RegisterPluginAsExtensions(nodeports.Name, nodePortsFactory, "PreFilter", "Filter"),
		func(_ *frameworkruntime.Registry, profile *schedulerconfig.KubeSchedulerProfile) {
			profile.PercentageOfNodesToScore = percentage
		},
	}
	fwk, err := schedulertesting.NewFramework(ctx, registeredPlugins, "koord-scheduler",
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
		frameworkruntime.WithSnapshotSharedLister(snapshot),
		frameworkruntime.WithPodNominator(internalqueue.NewTestQueue(ctx, nil)),
	)
	if err != nil {
		b.Fatal(err)
	}

	s := newEquivalenceScheduling(&scheduler.Scheduler{
		Cache: schedulerCache,
		Profiles: profile.Map{
			"koord-scheduler": fwk,
		},
	}, nil, defaultEquivalenceClassCacheSize)
	s.equivalence = newEquivalenceClassCache(time.Hour, defaultEquivalenceClassCacheSize)
	return s
}
