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

package sandboxequivalence

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fwktype "k8s.io/kube-scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	Name = "SandboxEquivalence"

	stateKey = Name
)

var (
	_ fwktype.PreFilterPlugin                = &Plugin{}
	_ fwktype.ReservePlugin                  = &Plugin{}
	_ frameworkext.ScoreResultRecorder       = &Plugin{}
	_ frameworkext.PreferNodesPluginProvider = &Plugin{}
	_ frameworkext.PreferNodesPlugin         = &Plugin{}
)

type cachedNode struct {
	name string
	// resourceVersion is the version of the node object when it was cached.
	resourceVersion string
}

// cacheEntry holds the feasible nodes of the last full scheduling cycle of an equivalence class, best first.
type cacheEntry struct {
	nodes    []cachedNode
	storedAt time.Time
}

// Plugin lets pods of the same sandbox template, an equivalence class, take nodes from the cache entry
// left by the class's last full scheduling cycle.
type Plugin struct {
	handle         fwktype.Handle
	maxCachedNodes int
	// cacheTTL bounds how long a cache entry is trusted, since scores also depend on inputs that do not
	// change the node object, such as the pods on the node.
	cacheTTL time.Duration
	// templateHashLabel is the pod label that identifies the equivalence class of a pod.
	templateHashLabel string
	// equivalenceCache maps the key of an equivalence class to its cache entry. It is only accessed in
	// the scheduling cycle, which runs serially.
	equivalenceCache map[string]*cacheEntry
}

// cacheKey returns the equivalence class of the pod, or "" if it has none. Some score plugins only count
// pods in the same namespace, so a class does not span namespaces.
func (p *Plugin) cacheKey(pod *corev1.Pod) string {
	hash := pod.Labels[p.templateHashLabel]
	if hash == "" {
		return ""
	}
	return pod.Namespace + "/" + hash
}

// stateData holds the equivalence class of the pod being scheduled.
type stateData struct {
	key string
}

func (s *stateData) Clone() fwktype.StateData {
	return s
}

// getCacheKey returns the equivalence class recorded in PreFilter, or "" if there is none.
func getCacheKey(cycleState fwktype.CycleState) string {
	data, err := cycleState.Read(stateKey)
	if err != nil {
		return ""
	}
	s, ok := data.(*stateData)
	if !ok {
		return ""
	}
	return s.key
}

func New(_ context.Context, args runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
	pluginArgs, ok := args.(*config.SandboxEquivalenceArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type SandboxEquivalenceArgs, got %T", args)
	}
	if err := validation.ValidateSandboxEquivalenceArgs(nil, pluginArgs); err != nil {
		return nil, err
	}
	return &Plugin{
		handle:            handle,
		maxCachedNodes:    int(pluginArgs.MaxCachedNodes),
		cacheTTL:          pluginArgs.CacheTTL.Duration,
		templateHashLabel: pluginArgs.TemplateHashLabel,
		equivalenceCache:  map[string]*cacheEntry{},
	}, nil
}

func (p *Plugin) Name() string {
	return Name
}

// PreFilter records the equivalence class of the pod for the later phases. Pods without one are skipped.
func (p *Plugin) PreFilter(_ context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, _ []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	key := p.cacheKey(pod)
	if key == "" {
		return nil, fwktype.NewStatus(fwktype.Skip)
	}
	cycleState.Write(stateKey, &stateData{key: key})
	return nil, nil
}

func (p *Plugin) PreFilterExtensions() fwktype.PreFilterExtensions {
	return nil
}

func (p *Plugin) PreferNodesPlugin() frameworkext.PreferNodesPlugin {
	return p
}

// PreferNodes hands out the best node left in the cache entry of the pod's equivalence class. The node stays
// in the entry until Reserve takes it out, so a hint rejected by Filter is kept unless the fallback full
// cycle replaces the entry.
func (p *Plugin) PreferNodes(_ context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, result *fwktype.PreFilterResult) ([]string, *fwktype.Status) {
	// The scheduler tries the nominated node before any hint.
	if pod.Status.NominatedNodeName != "" {
		return nil, fwktype.NewStatus(fwktype.Skip)
	}
	key := getCacheKey(cycleState)
	entry := p.equivalenceCache[key]
	if entry == nil {
		return nil, fwktype.NewStatus(fwktype.Skip)
	}
	if !p.hintable(entry, result) {
		delete(p.equivalenceCache, key)
		return nil, fwktype.NewStatus(fwktype.Skip)
	}
	return []string{entry.nodes[0].name}, nil
}

// hintable reports whether the head of the cache entry can be handed out to the pod.
func (p *Plugin) hintable(entry *cacheEntry, result *fwktype.PreFilterResult) bool {
	if time.Since(entry.storedAt) > p.cacheTTL || len(entry.nodes) == 0 {
		return false
	}
	head := entry.nodes[0]
	if !result.AllNodes() && !result.NodeNames.Has(head.name) {
		return false
	}
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(head.name)
	if err != nil {
		return false
	}
	return nodeInfo.Node().ResourceVersion == head.resourceVersion
}

// RecordScoreResult replaces the cache entry of the pod's equivalence class with the nodes ranked by
// a full scheduling cycle.
func (p *Plugin) RecordScoreResult(_ context.Context, cycleState fwktype.CycleState, _ *corev1.Pod, scores []fwktype.NodePluginScores) {
	key := getCacheKey(cycleState)
	if key == "" {
		return
	}
	sorted := slices.Clone(scores)
	slices.SortFunc(sorted, func(a, b fwktype.NodePluginScores) int {
		return cmp.Compare(b.TotalScore, a.TotalScore)
	})
	sorted = sorted[:min(len(sorted), p.maxCachedNodes)]

	nodes := make([]cachedNode, 0, len(sorted))
	for _, score := range sorted {
		nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(score.Name)
		if err != nil {
			continue
		}
		nodes = append(nodes, cachedNode{name: score.Name, resourceVersion: nodeInfo.Node().ResourceVersion})
	}
	// Entries of classes that no longer schedule pods are only dropped here.
	maps.DeleteFunc(p.equivalenceCache, func(_ string, e *cacheEntry) bool { return time.Since(e.storedAt) > p.cacheTTL })
	p.equivalenceCache[key] = &cacheEntry{nodes: nodes, storedAt: time.Now()}
}

// Reserve takes the chosen node out of the cache entry, so that a later pod of the class is not handed it again.
func (p *Plugin) Reserve(_ context.Context, cycleState fwktype.CycleState, _ *corev1.Pod, nodeName string) *fwktype.Status {
	if entry := p.equivalenceCache[getCacheKey(cycleState)]; entry != nil {
		entry.nodes = slices.DeleteFunc(entry.nodes, func(n cachedNode) bool { return n.name == nodeName })
	}
	return nil
}

func (p *Plugin) Unreserve(_ context.Context, _ fwktype.CycleState, _ *corev1.Pod, _ string) {}
