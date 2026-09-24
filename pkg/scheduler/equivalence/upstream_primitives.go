/*
Copyright 2022 The Koordinator Authors.
Copyright 2014 The Kubernetes Authors.

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

// This file contains verbatim copies of unexported functions from the upstream Kubernetes
// scheduler so the equivalence-class path can call them without importing the scheduler
// package's internals.
//
// Forked from k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go
//
// The functions below are kept body-identical to upstream. Two shapes appear here, both accepted
// by hack/verify-upstream-sync.sh after it normalizes them back to upstream's:
//
//   - Free functions upstream declares as free functions (prioritizeNodes,
//     findNodesThatPassExtenders): only import aliases differ (fwk→fwktype, v1→corev1).
//   - Methods upstream binds to *Scheduler (hasScoring, hasExtenderFilters): the receiver becomes
//     an explicit first parameter so the unexported method is reachable from outside the scheduler
//     package. The body is untouched.
//
// Do NOT add local logic here. If a function body needs local modifications, it belongs in
// equivalence_schedule.go with DIFF markers, not in this file. Note that numFeasibleNodesToFind is
// deliberately NOT here: upstream reads the unexported sched.percentageOfNodesToScore, which no
// caller outside the scheduler package can supply unchanged.
//
// Maintenance: after every k8s.io/kubernetes version upgrade, run hack/verify-upstream-sync.sh
// to detect any drift between this file and the upstream source.

package equivalence

import (
	"context"
	"math/rand"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
)

// hasScoring is a verbatim copy of the upstream unexported method
// (k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go:602). Upstream binds it to *Scheduler;
// here the receiver becomes an explicit first parameter so the body can stay byte-identical while
// remaining reachable from outside the scheduler package.
func hasScoring(sched *scheduler.Scheduler, fwk framework.Framework) bool {
	if fwk.HasScorePlugins() {
		return true
	}
	for _, extender := range sched.Extenders {
		if extender.IsPrioritizer() {
			return true
		}
	}
	return false
}

// hasExtenderFilters is a verbatim copy of the upstream unexported method
// (k8s.io/kubernetes@v1.35.6/pkg/scheduler/schedule_one.go:615), with the same
// receiver-to-first-parameter adaptation as hasScoring.
func hasExtenderFilters(sched *scheduler.Scheduler) bool {
	for _, extender := range sched.Extenders {
		if extender.IsFilter() {
			return true
		}
	}
	return false
}

// prioritizeNodes is a verbatim copy of the upstream unexported function.
func prioritizeNodes(
	ctx context.Context,
	extenders []fwktype.Extender,
	schedFramework framework.Framework,
	state fwktype.CycleState,
	pod *corev1.Pod,
	nodes []fwktype.NodeInfo,
) ([]fwktype.NodePluginScores, error) {
	logger := klog.FromContext(ctx)
	// If no priority configs are provided, then all nodes will have a score of one.
	// This is required to generate the priority list in the required format
	if len(extenders) == 0 && !schedFramework.HasScorePlugins() {
		result := make([]fwktype.NodePluginScores, 0, len(nodes))
		for i := range nodes {
			result = append(result, fwktype.NodePluginScores{
				Name:       nodes[i].Node().Name,
				TotalScore: 1,
			})
		}
		return result, nil
	}

	// Run PreScore plugins.
	preScoreStatus := schedFramework.RunPreScorePlugins(ctx, state, pod, nodes)
	if !preScoreStatus.IsSuccess() {
		return nil, preScoreStatus.AsError()
	}

	// Run the Score plugins.
	nodesScores, scoreStatus := schedFramework.RunScorePlugins(ctx, state, pod, nodes)
	if !scoreStatus.IsSuccess() {
		return nil, scoreStatus.AsError()
	}

	// Additional details logged at level 10 if enabled.
	loggerVTen := logger.V(10)
	if loggerVTen.Enabled() {
		for _, nodeScore := range nodesScores {
			for _, pluginScore := range nodeScore.Scores {
				loggerVTen.Info("Plugin scored node for pod", "pod", klog.KObj(pod), "plugin", pluginScore.Name, "node", nodeScore.Name, "score", pluginScore.Score)
			}
		}
	}

	if len(extenders) != 0 && nodes != nil {
		// allNodeExtendersScores has all extenders scores for all nodes.
		// It is keyed with node name.
		allNodeExtendersScores := make(map[string]*fwktype.NodePluginScores, len(nodes))
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := range extenders {
			if !extenders[i].IsInterested(pod) {
				continue
			}
			wg.Add(1)
			go func(extIndex int) {
				metrics.Goroutines.WithLabelValues(metrics.PrioritizingExtender).Inc()
				defer func() {
					metrics.Goroutines.WithLabelValues(metrics.PrioritizingExtender).Dec()
					wg.Done()
				}()
				prioritizedList, weight, err := extenders[extIndex].Prioritize(pod, nodes)
				if err != nil {
					// Prioritization errors from extender can be ignored, let k8s/other extenders determine the priorities
					logger.V(5).Info("Failed to run extender's priority function. No score given by this extender.", "error", err, "pod", klog.KObj(pod), "extender", extenders[extIndex].Name())
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for i := range *prioritizedList {
					nodename := (*prioritizedList)[i].Host
					score := (*prioritizedList)[i].Score
					if loggerVTen.Enabled() {
						loggerVTen.Info("Extender scored node for pod", "pod", klog.KObj(pod), "extender", extenders[extIndex].Name(), "node", nodename, "score", score)
					}

					// MaxExtenderPriority may diverge from the max priority used in the scheduler and defined by MaxNodeScore,
					// therefore we need to scale the score returned by extenders to the score range used by the scheduler.
					finalscore := score * weight * (fwktype.MaxNodeScore / extenderv1.MaxExtenderPriority)

					if allNodeExtendersScores[nodename] == nil {
						allNodeExtendersScores[nodename] = &fwktype.NodePluginScores{
							Name:   nodename,
							Scores: make([]fwktype.PluginScore, 0, len(extenders)),
						}
					}
					allNodeExtendersScores[nodename].Scores = append(allNodeExtendersScores[nodename].Scores, fwktype.PluginScore{
						Name:  extenders[extIndex].Name(),
						Score: finalscore,
					})
					allNodeExtendersScores[nodename].TotalScore += finalscore
				}
			}(i)
		}
		// wait for all go routines to finish
		wg.Wait()
		for i := range nodesScores {
			if score, ok := allNodeExtendersScores[nodes[i].Node().Name]; ok {
				nodesScores[i].Scores = append(nodesScores[i].Scores, score.Scores...)
				nodesScores[i].TotalScore += score.TotalScore
				nodesScores[i].Randomizer = rand.Int()
			}
		}
	}

	if loggerVTen.Enabled() {
		for i := range nodesScores {
			loggerVTen.Info("Calculated node's final score for pod", "pod", klog.KObj(pod), "node", nodesScores[i].Name, "score", nodesScores[i].TotalScore)
		}
	}
	return nodesScores, nil
}

// findNodesThatPassExtenders is a verbatim copy of the upstream unexported function.
func findNodesThatPassExtenders(ctx context.Context, extenders []fwktype.Extender, pod *corev1.Pod, feasibleNodes []fwktype.NodeInfo, statuses *framework.NodeToStatus) ([]fwktype.NodeInfo, error) {
	logger := klog.FromContext(ctx)

	// Extenders are called sequentially.
	// Nodes in original feasibleNodes can be excluded in one extender, and pass on to the next
	// extender in a decreasing manner.
	for _, extender := range extenders {
		if len(feasibleNodes) == 0 {
			break
		}
		if !extender.IsInterested(pod) {
			continue
		}

		// Status of failed nodes in failedAndUnresolvableMap will be added to <statuses>,
		// so that the scheduler framework can respect the UnschedulableAndUnresolvable status for
		// particular nodes, and this may eventually improve preemption efficiency.
		// Note: users are recommended to configure the extenders that may return UnschedulableAndUnresolvable
		// status ahead of others.
		feasibleList, failedMap, failedAndUnresolvableMap, err := extender.Filter(pod, feasibleNodes)
		if err != nil {
			if extender.IsIgnorable() {
				logger.Info("Skipping extender as it returned error and has ignorable flag set", "extender", extender, "err", err)
				continue
			}
			return nil, err
		}

		for failedNodeName, failedMsg := range failedAndUnresolvableMap {
			statuses.Set(failedNodeName, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, failedMsg))
		}

		for failedNodeName, failedMsg := range failedMap {
			if _, found := failedAndUnresolvableMap[failedNodeName]; found {
				// failedAndUnresolvableMap takes precedence over failedMap
				// note that this only happens if the extender returns the node in both maps
				continue
			}
			statuses.Set(failedNodeName, fwktype.NewStatus(fwktype.Unschedulable, failedMsg))
		}

		feasibleNodes = feasibleList
	}
	return feasibleNodes, nil
}
