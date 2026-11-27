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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	configv1 "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1"
)

// buildArgs builds the plugin args from the given v1 args via the v1 defaulting and conversion.
func buildArgs(t *testing.T, v1Args configv1.SandboxEquivalenceArgs) *config.SandboxEquivalenceArgs {
	configv1.SetDefaults_SandboxEquivalenceArgs(&v1Args)
	var args config.SandboxEquivalenceArgs
	assert.NoError(t, configv1.Convert_v1_SandboxEquivalenceArgs_To_config_SandboxEquivalenceArgs(&v1Args, &args, nil))
	return &args
}

func TestDefaultArgs(t *testing.T) {
	args := buildArgs(t, configv1.SandboxEquivalenceArgs{})
	assert.Equal(t, int32(100), args.MaxCachedNodes)
	assert.Equal(t, 500*time.Millisecond, args.CacheTTL.Duration)
	assert.Equal(t, extension.LabelSandboxTemplateHash, args.TemplateHashLabel)
}

func TestPlugin_PreFilter(t *testing.T) {
	tests := []struct {
		name     string
		v1Args   configv1.SandboxEquivalenceArgs
		labels   map[string]string
		wantCode fwktype.Code
		wantKey  string
	}{
		{
			name:     "pod with the template hash records its equivalence class",
			labels:   map[string]string{extension.LabelSandboxTemplateHash: "template-1"},
			wantCode: fwktype.Success,
			wantKey:  "default/template-1",
		},
		{
			name:     "pod without the template hash is skipped",
			wantCode: fwktype.Skip,
			wantKey:  "",
		},
		{
			name:   "pod is skipped when the hash is on another label",
			labels: map[string]string{"app.kubernetes.io/instance": "template-1"},
			v1Args: configv1.SandboxEquivalenceArgs{
				TemplateHashLabel: ptr.To("app.kubernetes.io/instance"),
			},
			wantCode: fwktype.Success,
			wantKey:  "default/template-1",
		},
		{
			name:   "pod is skipped when the configured label is missing",
			labels: map[string]string{extension.LabelSandboxTemplateHash: "template-1"},
			v1Args: configv1.SandboxEquivalenceArgs{
				TemplateHashLabel: ptr.To("app.kubernetes.io/instance"),
			},
			wantCode: fwktype.Skip,
			wantKey:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl, err := New(context.TODO(), buildArgs(t, tt.v1Args), nil)
			assert.NoError(t, err)
			p := pl.(*Plugin)
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "pod-1", Labels: tt.labels}}
			cycleState := framework.NewCycleState()

			result, status := p.PreFilter(context.TODO(), cycleState, pod, nil)
			assert.Nil(t, result)
			assert.Equal(t, tt.wantCode, status.Code())
			assert.Equal(t, tt.wantKey, getCacheKey(cycleState))
		})
	}
}

func TestNew_InvalidTemplateHashLabel(t *testing.T) {
	_, err := New(context.TODO(), buildArgs(t, configv1.SandboxEquivalenceArgs{
		TemplateHashLabel: ptr.To("!!invalid"),
	}), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "templateHashLabel")
}

func TestPlugin_RecordScoreResult_WithoutEquivalenceClass(t *testing.T) {
	pl, err := New(context.TODO(), buildArgs(t, configv1.SandboxEquivalenceArgs{}), nil)
	assert.NoError(t, err)
	p := pl.(*Plugin)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "pod-1",
			Labels:    map[string]string{extension.LabelSandboxTemplateHash: "template-1"},
		},
	}

	// The cycle state carries no equivalence class when PreFilter does not run for the plugin.
	p.RecordScoreResult(context.TODO(), framework.NewCycleState(), pod, []fwktype.NodePluginScores{{Name: "node-1", TotalScore: 10}})
	assert.Empty(t, p.equivalenceCache)
}

// testHandle serves the snapshot reads of the plugin.
type testHandle struct {
	fwktype.Handle
	snapshot fwktype.SharedLister
}

func (h *testHandle) SnapshotSharedLister() fwktype.SharedLister {
	return h.snapshot
}

func TestPlugin_PreferNodes(t *testing.T) {
	var nodes []*corev1.Node
	for _, name := range []string{"node-1", "node-2", "node-3", "node-4"} {
		nodes = append(nodes, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, ResourceVersion: "1"}})
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "pod-1",
			Labels:    map[string]string{extension.LabelSandboxTemplateHash: "template-1"},
		},
	}
	tests := []struct {
		name string
		// fallbackScores and reservedNode describe the cycle that is handed node-2: the scores of the full
		// cycle run after the hint is rejected, and the node the pod is reserved on, "" if no node fits.
		fallbackScores []fwktype.NodePluginScores
		reservedNode   string
		wantNodes      []string
	}{
		{
			name:         "a used hint is taken out of the entry",
			reservedNode: "node-2",
			wantNodes:    []string{"node-3"},
		},
		{
			name:           "a rejected hint is replaced by the ranking of the fallback cycle",
			fallbackScores: []fwktype.NodePluginScores{{Name: "node-4", TotalScore: 50}, {Name: "node-1", TotalScore: 40}},
			reservedNode:   "node-4",
			wantNodes:      []string{"node-1"},
		},
		{
			name:         "a rejected hint stays at the head when the fallback cycle finds a single node",
			reservedNode: "node-4",
			wantNodes:    []string{"node-2"},
		},
		{
			name:      "a rejected hint stays at the head when no node fits",
			wantNodes: []string{"node-2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			pl, err := New(ctx, buildArgs(t, configv1.SandboxEquivalenceArgs{}), &testHandle{snapshot: cache.NewSnapshot(nil, nodes)})
			assert.NoError(t, err)
			p := pl.(*Plugin)

			// A full cycle ranks node-1 to node-4 and reserves node-1.
			cycleState := framework.NewCycleState()
			_, status := p.PreFilter(ctx, cycleState, pod, nil)
			assert.True(t, status.IsSuccess())
			p.RecordScoreResult(ctx, cycleState, pod, []fwktype.NodePluginScores{
				{Name: "node-1", TotalScore: 40}, {Name: "node-2", TotalScore: 30},
				{Name: "node-3", TotalScore: 20}, {Name: "node-4", TotalScore: 10},
			})
			assert.True(t, p.Reserve(ctx, cycleState, pod, "node-1").IsSuccess())

			cycleState = framework.NewCycleState()
			_, status = p.PreFilter(ctx, cycleState, pod, nil)
			assert.True(t, status.IsSuccess())
			gotNodes, status := p.PreferNodes(ctx, cycleState, pod, nil)
			assert.True(t, status.IsSuccess())
			assert.Equal(t, []string{"node-2"}, gotNodes)
			if tt.fallbackScores != nil {
				p.RecordScoreResult(ctx, cycleState, pod, tt.fallbackScores)
			}
			if tt.reservedNode != "" {
				assert.True(t, p.Reserve(ctx, cycleState, pod, tt.reservedNode).IsSuccess())
			}

			cycleState = framework.NewCycleState()
			_, status = p.PreFilter(ctx, cycleState, pod, nil)
			assert.True(t, status.IsSuccess())
			gotNodes, status = p.PreferNodes(ctx, cycleState, pod, nil)
			assert.True(t, status.IsSuccess())
			assert.Equal(t, tt.wantNodes, gotNodes)
		})
	}
}
