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
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
)

var (
	_ PreFilterTransformer = &TestTransformer{}
	_ FilterTransformer    = &TestTransformer{}
	_ ScoreTransformer     = &TestTransformer{}
)

type TestTransformer struct {
	index int
}

func (h *TestTransformer) Name() string { return "TestTransformer" }

func (h *TestTransformer) BeforePreFilter(handle ExtendedHandle, state *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("%d", h.index)] = fmt.Sprintf("%d", h.index)
	return pod, true
}

func (h *TestTransformer) BeforeFilter(handle ExtendedHandle, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) (*corev1.Pod, *framework.NodeInfo, bool) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("%d", h.index)] = fmt.Sprintf("%d", h.index)
	return pod, nodeInfo, true
}

func (h *TestTransformer) BeforeScore(handle ExtendedHandle, cycleState *framework.CycleState, pod *corev1.Pod, nodes []*corev1.Node) (*corev1.Pod, []*corev1.Node, bool) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("%d", h.index)] = fmt.Sprintf("%d", h.index)
	return pod, nodes, true
}

func Test_frameworkExtenderImpl_RunPreFilterPlugins(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want *framework.Status
	}{
		{
			name: "normal RunPreFilterPlugins",
			pod:  &corev1.Pod{},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testTransformers := []SchedulingTransformer{
				&TestTransformer{index: 1},
				&TestTransformer{index: 2},
			}
			extenderFactory, _ := NewFrameworkExtenderFactory(WithDefaultTransformers(testTransformers...))
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
			)
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
			assert.Equal(t, tt.want, frameworkExtender.RunPreFilterPlugins(context.TODO(), framework.NewCycleState(), tt.pod))
			assert.Len(t, tt.pod.Annotations, 2)
			assert.Equal(t, "1", tt.pod.Annotations["1"])
			assert.Equal(t, "2", tt.pod.Annotations["2"])
		})
	}
}

func Test_frameworkExtenderImpl_RunFilterPluginsWithNominatedPods(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		nodeInfo *framework.NodeInfo
		want     *framework.Status
	}{
		{
			name:     "normal RunFilterPluginsWithNominatedPods",
			pod:      &corev1.Pod{},
			nodeInfo: framework.NewNodeInfo(),
			want:     nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testTransformers := []SchedulingTransformer{
				&TestTransformer{index: 1},
				&TestTransformer{index: 2},
			}
			extenderFactory, _ := NewFrameworkExtenderFactory(WithDefaultTransformers(testTransformers...))
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
			)
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
			assert.Equal(t, tt.want, frameworkExtender.RunFilterPluginsWithNominatedPods(context.TODO(), framework.NewCycleState(), tt.pod, tt.nodeInfo))
			assert.Len(t, tt.pod.Annotations, 2)
			assert.Equal(t, "1", tt.pod.Annotations["1"])
			assert.Equal(t, "2", tt.pod.Annotations["2"])
		})
	}
}

func Test_frameworkExtenderImpl_RunScorePlugins(t *testing.T) {
	tests := []struct {
		name       string
		pod        *corev1.Pod
		nodes      []*corev1.Node
		wantScore  framework.PluginToNodeScores
		wantStatus *framework.Status
	}{
		{
			name:       "normal RunScorePlugins",
			pod:        &corev1.Pod{},
			nodes:      []*corev1.Node{{}},
			wantScore:  framework.PluginToNodeScores{},
			wantStatus: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testTransformers := []SchedulingTransformer{
				&TestTransformer{index: 1},
				&TestTransformer{index: 2},
			}
			extenderFactory, _ := NewFrameworkExtenderFactory(WithDefaultTransformers(testTransformers...))
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
			)
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
			score, status := frameworkExtender.RunScorePlugins(context.TODO(), framework.NewCycleState(), tt.pod, tt.nodes)
			assert.Equal(t, tt.wantScore, score)
			assert.Equal(t, tt.wantStatus, status)
			assert.Len(t, tt.pod.Annotations, 2)
			assert.Equal(t, "1", tt.pod.Annotations["1"])
			assert.Equal(t, "2", tt.pod.Annotations["2"])
		})
	}
}
