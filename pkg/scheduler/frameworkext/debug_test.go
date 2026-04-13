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

package frameworkext

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func TestDebugScoresSetter(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{
			name:    "integer",
			value:   "123",
			wantErr: false,
		},
		{
			name:    "float",
			value:   "11.22",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DebugScoresSetter(tt.value)
			if tt.wantErr && err == nil {
				t.Error("expected error but got nil")
			} else if !tt.wantErr && err != nil {
				t.Errorf("expected no error but got err: %v", err)
			}
		})
	}
}

func TestDebugFiltersSetter(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
		want    bool
	}{
		{
			name:    "valid bool",
			value:   "true",
			wantErr: false,
			want:    true,
		},
		{
			name:    "invalid",
			value:   "11.22",
			wantErr: true,
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DebugFiltersSetter(tt.value)
			if tt.wantErr && err == nil {
				t.Error("expected error but got nil")
			} else if !tt.wantErr && err != nil {
				t.Errorf("expected no error but got err: %v", err)
			}
			assert.Equal(t, tt.want, debugFilterFailure)
			debugFilterFailure = false
		})
	}
}

func TestDebugScores(t *testing.T) {
	pluginToNodeScores := map[string]fwktype.NodeScoreList{
		"ImageLocality": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 0},
		},
		"InterPodAffinity": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 0},
		},
		"LoadAwareScheduling": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 85},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 87},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 55},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 15},
		},
		"NodeAffinity": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 0},
		},
		"NodeNUMAResource": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 0},
		},
		"NodeResourcesBalancedAllocation": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 96},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 96},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 95},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 90},
		},
		"NodeResourcesFit": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 93},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 94},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 91},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 82},
		},
		"PodTopologySpread": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 200},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 200},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 200},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 200},
		},
		"Reservation": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 0},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 0},
		},
		"TaintToleration": {
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.50", Score: 100},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.51", Score: 100},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.19", Score: 100},
			fwktype.NodeScore{Name: "cn-hangzhou.10.0.4.18", Score: 100},
		},
	}
	nodeObjs := []*corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "cn-hangzhou.10.0.4.50"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "cn-hangzhou.10.0.4.51"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "cn-hangzhou.10.0.4.19"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "cn-hangzhou.10.0.4.18"}},
	}
	nodes := make([]fwktype.NodeInfo, 0, len(nodeObjs))
	for _, n := range nodeObjs {
		ni := framework.NewNodeInfo()
		ni.SetNode(n)
		nodes = append(nodes, ni)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "curlimage-545745d8f8-rngp7",
		},
	}

	m := map[string][]fwktype.PluginScore{}
	for pluginName, nodeScores := range pluginToNodeScores {
		for _, v := range nodeScores {
			m[v.Name] = append(m[v.Name], fwktype.PluginScore{
				Name:  pluginName,
				Score: v.Score,
			})
		}
	}
	allNodePluginScores := make([]fwktype.NodePluginScores, 0, len(m))
	for nodeName, pluginScores := range m {
		sort.Slice(pluginScores, func(i, j int) bool {
			return pluginScores[i].Name < pluginScores[j].Name
		})
		var totalScore int64
		for _, v := range pluginScores {
			totalScore += v.Score
		}
		allNodePluginScores = append(allNodePluginScores, fwktype.NodePluginScores{
			Name:       nodeName,
			Scores:     pluginScores,
			TotalScore: totalScore,
		})
	}

	w := debugScores(4, pod, allNodePluginScores, nodes)
	expectedResult := `| # | Pod | Node | Score | ImageLocality | InterPodAffinity | LoadAwareScheduling | NodeAffinity | NodeNUMAResource | NodeResourcesBalancedAllocation | NodeResourcesFit | PodTopologySpread | Reservation | TaintToleration |
| --- | --- | --- | ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:|
| 0 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.51 | 577 | 0 | 0 | 87 | 0 | 0 | 96 | 94 | 200 | 0 | 100 |
| 1 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.50 | 574 | 0 | 0 | 85 | 0 | 0 | 96 | 93 | 200 | 0 | 100 |
| 2 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.19 | 541 | 0 | 0 | 55 | 0 | 0 | 95 | 91 | 200 | 0 | 100 |
| 3 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.18 | 487 | 0 | 0 | 15 | 0 | 0 | 90 | 82 | 200 | 0 | 100 |`
	assert.Equal(t, expectedResult, w.RenderMarkdown())
}
