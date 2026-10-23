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

// Package sandbox assembles equivalence-class node selection and binding concurrency limiting for
// sandbox workloads. It owns the sandbox-specific semantics — which pods are sandbox pods and what
// makes them scheduling-equivalent — and wires the reusable machinery the equivalence and
// bindinglimiter packages provide. Nothing here implements scheduling or limiting logic itself.
package sandbox

import (
	"fmt"

	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	"k8s.io/kubernetes/pkg/scheduler"

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/bindinglimiter"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/equivalence"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	// LabelSandbox marks a pod as a sandbox workload. Sandbox pods are routed into the
	// dedicated sandbox scheduling path when sandbox equivalence scheduling is enabled.
	LabelSandbox = extension.SchedulingDomainPrefix + "/sandbox"

	// LabelSandboxTemplateHash identifies the sandbox template a pod is created from. Pods
	// carrying the same hash are treated as scheduling-equivalent by sandbox equivalence scheduling:
	// the scheduling decision computed for one pod of the class can be reused by the others.
	// It is written by the sandbox controller/adapter, which MUST guarantee that pods sharing
	// a hash are equivalent in every scheduling-relevant field. The value must be a valid
	// label value (at most 63 characters).
	//
	// The hash must cover at least: container, init-container and pod-level resource requests
	// and limits, runtimeClassName/overhead, priority, nodeSelector, node affinity, tolerations,
	// pod (anti-)affinity, topologySpreadConstraints, and the pod labels that other pods'
	// affinity or spread selectors may match. The scheduler profile is not part of the hash:
	// decisions are already cached per profile.
	//
	// The contract bounds placement quality, not feasibility. Cache hits do not run PreScore or
	// Score: pods of a class use the score order of the full cycle that cached the class, as
	// --sandbox-equivalence-placement selects. Each hit runs per-pod PreFilter, Filter and extender
	// filters. A pod that breaks the contract therefore costs a cache miss or a less preferred
	// node, never an infeasible placement.
	LabelSandboxTemplateHash = extension.SchedulingDomainPrefix + "/sandbox-template-hash"
)

var (
	// MaxConcurrentBindings is the number of sandbox binding cycles allowed to run concurrently
	// across all scheduler profiles.
	MaxConcurrentBindings = bindinglimiter.DefaultMaxConcurrentBindings
	// EquivalenceCacheSize is the number of sandbox equivalence classes whose scheduling decision
	// is cached.
	EquivalenceCacheSize = equivalence.DefaultEquivalenceClassCacheSize
	// EquivalencePlacement selects how sandbox pods of a class use the cached nodes. Spread is the
	// default because sandbox pods can be CPU-heavy, and packing them turns nodes into hotspots.
	EquivalencePlacement = string(equivalence.PlacementSpread)
)

// AddFlags registers the sandbox scheduling command-line flags.
func AddFlags(fs *pflag.FlagSet) {
	fs.IntVar(&MaxConcurrentBindings, "sandbox-max-concurrent-bindings", MaxConcurrentBindings,
		"Maximum number of sandbox binding cycles allowed to run concurrently across all scheduler profiles.")
	fs.IntVar(&EquivalenceCacheSize, "sandbox-equivalence-cache-size", EquivalenceCacheSize,
		"Maximum number of sandbox equivalence classes whose scheduling decision is cached.")
	fs.StringVar(&EquivalencePlacement, "sandbox-equivalence-placement", EquivalencePlacement,
		"Placement of sandbox pods served from a cached equivalence-class decision, which skip Score. "+
			"'spread' rotates through the cached nodes in score order; 'pack' keeps filling the node the previous pod of the class went to.")
}

// IsSandboxPod returns true if the pod is marked as a sandbox workload.
func IsSandboxPod(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	return pod.Labels[LabelSandbox] == "true"
}

// GetSandboxTemplateHash returns the sandbox template hash of the pod, or "" if the pod does
// not carry one. Only sandbox pods with a non-empty hash are eligible for equivalence-class
// decision reuse.
func GetSandboxTemplateHash(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	return pod.Labels[LabelSandboxTemplateHash]
}

// IsSandboxActive returns true for the sandbox pods that take part in equivalence-class reuse:
// marked as sandbox workloads and carrying a template hash to be grouped by.
func IsSandboxActive(pod *corev1.Pod) bool {
	return IsSandboxPod(pod) && GetSandboxTemplateHash(pod) != ""
}

// sandboxClass is the sandbox workload's grouping contract. It is the only place the reusable
// modules learn what "the same sandbox" means: a pod is in the class when it is an active sandbox
// pod, and its identity is the template hash written by the sandbox controller/adapter.
type sandboxClass struct{}

func (sandboxClass) Handles(pod *corev1.Pod) bool {
	return IsSandboxActive(pod)
}

func (sandboxClass) Key(pod *corev1.Pod) string {
	return GetSandboxTemplateHash(pod)
}

var _ frameworkext.EquivalenceClass = sandboxClass{}

// Setup wires equivalence-class node selection and shared binding admission onto every profile's
// EquivalenceAwareHandle, so FrameworkExtenderFactory.scheduleOne routes sandbox pods through the
// equivalence path and the upstream binding cycle bounds their concurrency through the limiter.
// It wires nothing when the feature gate is off, leaving the upstream scheduling path untouched.
func Setup(sched *scheduler.Scheduler, informerFactory informers.SharedInformerFactory, percentageOfNodesToScore *int32) error {
	if !feature.DefaultFeatureGate.Enabled(koordfeatures.EnableSandboxEquivalenceScheduling) {
		return nil
	}
	// The equivalence-class path and the inline batch scheduler both take over pods that would
	// otherwise flow through FrameworkExtenderFactory.scheduleOne. Keep them mutually exclusive so a
	// single pod is never claimed by both takeovers.
	if feature.DefaultFeatureGate.Enabled(koordfeatures.EnableInlineBatchSchedule) {
		return fmt.Errorf("feature gates %s and %s are mutually exclusive",
			koordfeatures.EnableSandboxEquivalenceScheduling, koordfeatures.EnableInlineBatchSchedule)
	}
	if MaxConcurrentBindings <= 0 {
		return fmt.Errorf("sandbox max concurrent bindings must be greater than 0")
	}
	if EquivalenceCacheSize <= 0 {
		return fmt.Errorf("sandbox equivalence cache size must be greater than 0")
	}
	placement := equivalence.PlacementPolicy(EquivalencePlacement)
	if placement != equivalence.PlacementSpread && placement != equivalence.PlacementPack {
		return fmt.Errorf("sandbox equivalence placement must be %q or %q, got %q",
			equivalence.PlacementSpread, equivalence.PlacementPack, EquivalencePlacement)
	}
	// One class feeds both modules so membership and identity cannot diverge between them.
	class := sandboxClass{}
	scheduling := equivalence.NewEquivalenceScheduling(sched, percentageOfNodesToScore, EquivalenceCacheSize, placement, class)
	// One limiter instance is shared across all profiles so its semaphore bounds the total number
	// of concurrent sandbox binding cycles, not the number per profile.
	limiter := bindinglimiter.NewBindingLimiter(MaxConcurrentBindings, class)

	if err := scheduling.RegisterNodeEventHandler(informerFactory.Core().V1().Nodes().Informer()); err != nil {
		return err
	}

	for _, fwk := range sched.Profiles {
		if handle, ok := fwk.(frameworkext.EquivalenceAwareHandle); ok {
			handle.RegisterSchedulingDecisionProvider(scheduling)
			handle.SetBindingLimiter(limiter)
		}
	}
	return nil
}
