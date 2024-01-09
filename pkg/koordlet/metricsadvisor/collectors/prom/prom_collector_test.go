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

package prom

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	mockmetriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mockstatesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestCollector(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		statesInformer := mockstatesinformer.NewMockStatesInformer(ctrl)
		metricCache := mockmetriccache.NewMockMetricCache(ctrl)
		appender := mockmetriccache.NewMockAppender(ctrl)
		helper := system.NewFileTestUtil(t)
		defer helper.Cleanup()
		testRuleContent := `
---
enable: true
jobs:
  - name: job-0
    enable: true
    address: "127.0.0.1"
    port: 8000
    path: /metrics
    scrapeInterval: 20s
    scrapeTimeout: 5s
    metrics:
    - sourceName: metric_a
      targetName: metric_a
      targetType: "node"
    - sourceName: metric_b
      sourceLabels:
        bbb: aaa
      targetName: metric_b
      targetType: "node"
  - name: job-1
    enable: true
    address: "127.0.0.1"
    port: 9316
    path: /metrics
    scrapeInterval: 30s
    scrapeTimeout: 10s
    metrics:
    - sourceName: metric_c
      targetName: metric_c
      targetType: "pod"
  - name: job-2
    enable: false
    address: "127.0.0.1"
    port: 9090
    path: /default-metrics
    scrapeInterval: 30s
    scrapeTimeout: 10s
    metrics:
    - sourceName: metric_d
      targetName: metric_d
      targetType: "pod"
`
		helper.WriteFileContents("prom-collect-rule.yaml", testRuleContent)
		testNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node",
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100"),
					corev1.ResourceMemory: resource.MustParse("400Gi"),
				},
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100"),
					corev1.ResourceMemory: resource.MustParse("400Gi"),
				},
			},
		}
		testPodMetas := []*statesinformer.PodMeta{
			{
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-be-pod",
						Namespace: "test-ns",
						UID:       "uid-xxxxxx",
						Labels: map[string]string{
							extension.LabelPodQoS: string(extension.QoSBE),
						},
					},
				},
			},
			{
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ls-pod",
						Namespace: "test-ns",
						UID:       "uid-yyyyyy",
						Labels: map[string]string{
							extension.LabelPodQoS: string(extension.QoSLS),
						},
					},
				},
			},
		}

		c := New(&framework.Options{
			MetricCache:    metricCache,
			StatesInformer: statesInformer,
			Config: &framework.Config{
				CollectPromMetricRulePath: helper.TempDir + "/prom-collect-rule.yaml",
			},
		})
		assert.NotNil(t, c)
		assert.True(t, c.Enabled())
		c.Setup(&framework.Context{})

		statesInformer.EXPECT().HasSynced().Return(true)
		statesInformer.EXPECT().GetNode().Return(testNode).AnyTimes()
		statesInformer.EXPECT().GetAllPods().Return(testPodMetas).AnyTimes()
		metricCache.EXPECT().Appender().Return(appender).AnyTimes()
		appender.EXPECT().Append(gomock.Any()).Return(nil).AnyTimes()
		appender.EXPECT().Commit().Return(nil).AnyTimes()
		stopCh := make(chan struct{})
		defer close(stopCh)
		c.Run(stopCh)
	})
}
