/*
 * Copyright 2022 The Koordinator Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package resmanager

// var podsResource = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
//
// func Test_memoryEvict(t *testing.T) {
// 	type args struct {
// 		name               string
// 		node               *corev1.Node
// 		nodeMetric         *metriccache.NodeResourceMetric
// 		podMetrics         []*metriccache.PodResourceMetric
// 		pods               []*corev1.Pod
// 		thresholdConfig    *v1alpha1.ResourceThresholdStrategy
// 		expectEvictPods    []*corev1.Pod
// 		expectNotEvictPods []*corev1.Pod
// 	}
//
// 	tests := []args{
// 		{
// 			name: "test_memoryevict_no_thresholdConfig",
// 		},
// 		{
// 			name: "test_MemoryEvictThresholdPercent_not_valid",
// 			// invalid
// 			thresholdConfig: &v1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64Ptr(-1)},
// 		},
// 		{
// 			name:            "test_nodeMetric_nil",
// 			thresholdConfig: &v1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64Ptr(80)},
// 		},
// 		{
// 			name: "test_node_nil",
// 			nodeMetric: &metriccache.NodeResourceMetric{
// 				MemoryUsed: metriccache.MemoryMetric{
// 					MemoryWithoutCache: resource.MustParse("115G"),
// 				},
// 			},
// 			thresholdConfig: &v1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64Ptr(80)},
// 		},
// 		{
// 			name: "test_node_memorycapacity_invalid",
// 			node: getNode("80", "0"),
// 			nodeMetric: &metriccache.NodeResourceMetric{
// 				MemoryUsed: metriccache.MemoryMetric{
// 					MemoryWithoutCache: resource.MustParse("115G"),
// 				},
// 			},
// 			thresholdConfig: &v1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64Ptr(80)},
// 		},
// 		{
// 			name: "test_memory_under_evict_line",
// 			node: getNode("80", "120G"),
// 			pods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_lsr_pod", extension.QoSLSR, 1000),
// 				createMemoryEvictTestPod("test_ls_pod", extension.QoSLS, 500),
// 				createMemoryEvictTestPod("test_noqos_pod", extension.QoSNone, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_1", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_2", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority120", extension.QoSBE, 120),
// 			},
// 			nodeMetric: &metriccache.NodeResourceMetric{
// 				MemoryUsed: metriccache.MemoryMetric{
// 					MemoryWithoutCache: resource.MustParse("80G"),
// 				},
// 			},
// 			podMetrics: []*metriccache.PodResourceMetric{
// 				createPodResourceMetric("test_lsr_pod", "30G"),
// 				createPodResourceMetric("test_ls_pod", "20G"),
// 				createPodResourceMetric("test_noqos_pod", "10G"),
// 				createPodResourceMetric("test_be_pod_priority100_1", "4G"),
// 				createPodResourceMetric("test_be_pod_priority100_2", "8G"),
// 				createPodResourceMetric("test_be_pod_priority120", "8G"),
// 			},
// 			thresholdConfig: &v1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64Ptr(80)},
// 			expectEvictPods: []*corev1.Pod{},
// 			expectNotEvictPods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_lsr_pod", extension.QoSLSR, 1000),
// 				createMemoryEvictTestPod("test_ls_pod", extension.QoSLS, 500),
// 				createMemoryEvictTestPod("test_noqos_pod", extension.QoSNone, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_1", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_2", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority120", extension.QoSBE, 120),
// 			},
// 		},
// 		{
// 			name: "test_memoryevict_MemoryEvictThresholdPercent_sort_by_priority_and_usage_82",
// 			node: getNode("80", "120G"),
// 			pods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_lsr_pod", extension.QoSLSR, 1000),
// 				createMemoryEvictTestPod("test_ls_pod", extension.QoSLS, 500),
// 				createMemoryEvictTestPod("test_noqos_pod", extension.QoSNone, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_1", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_2", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority120", extension.QoSBE, 120),
// 			},
// 			nodeMetric: &metriccache.NodeResourceMetric{
// 				MemoryUsed: metriccache.MemoryMetric{
// 					MemoryWithoutCache: resource.MustParse("115G"),
// 				},
// 			},
// 			podMetrics: []*metriccache.PodResourceMetric{
// 				createPodResourceMetric("test_lsr_pod", "40G"),
// 				createPodResourceMetric("test_ls_pod", "30G"),
// 				createPodResourceMetric("test_noqos_pod", "10G"),
// 				createPodResourceMetric("test_be_pod_priority100_1", "5G"),
// 				createPodResourceMetric("test_be_pod_priority100_2", "20G"), // evict
// 				createPodResourceMetric("test_be_pod_priority120", "10G"),
// 			},
// 			thresholdConfig: &v1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64Ptr(82)}, // >96G
// 			expectEvictPods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_be_pod_priority100_2", extension.QoSBE, 100),
// 			},
// 			expectNotEvictPods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_lsr_pod", extension.QoSLSR, 1000),
// 				createMemoryEvictTestPod("test_ls_pod", extension.QoSLS, 500),
// 				createMemoryEvictTestPod("test_noqos_pod", extension.QoSNone, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_1", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority120", extension.QoSBE, 120),
// 			},
// 		},
// 		{
// 			name: "test_memoryevict_MemoryEvictThresholdPercent_80",
// 			node: getNode("80", "120G"),
// 			pods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_lsr_pod", extension.QoSLSR, 1000),
// 				createMemoryEvictTestPod("test_ls_pod", extension.QoSLS, 500),
// 				createMemoryEvictTestPod("test_noqos_pod", extension.QoSNone, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_1", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_2", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority120", extension.QoSBE, 120),
// 			},
// 			nodeMetric: &metriccache.NodeResourceMetric{
// 				MemoryUsed: metriccache.MemoryMetric{
// 					MemoryWithoutCache: resource.MustParse("115G"),
// 				},
// 			},
// 			podMetrics: []*metriccache.PodResourceMetric{
// 				createPodResourceMetric("test_lsr_pod", "40G"),
// 				createPodResourceMetric("test_ls_pod", "30G"),
// 				createPodResourceMetric("test_noqos_pod", "10G"),
// 				createPodResourceMetric("test_be_pod_priority100_1", "5G"),  // evict
// 				createPodResourceMetric("test_be_pod_priority100_2", "20G"), // evict
// 				createPodResourceMetric("test_be_pod_priority120", "10G"),
// 			},
// 			thresholdConfig: &v1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64Ptr(80)}, // >91.2G
// 			expectEvictPods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_be_pod_priority100_2", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_1", extension.QoSBE, 100),
// 			},
// 			expectNotEvictPods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_lsr_pod", extension.QoSLSR, 1000),
// 				createMemoryEvictTestPod("test_ls_pod", extension.QoSLS, 500),
// 				createMemoryEvictTestPod("test_noqos_pod", extension.QoSNone, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority120", extension.QoSBE, 120),
// 			},
// 		},
// 		{
// 			name: "test_memoryevict_MemoryEvictThresholdPercent_50",
// 			node: getNode("80", "120G"),
// 			pods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_lsr_pod", extension.QoSLSR, 1000),
// 				createMemoryEvictTestPod("test_ls_pod", extension.QoSLS, 500),
// 				createMemoryEvictTestPod("test_noqos_pod", extension.QoSNone, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_1", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_2", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority120", extension.QoSBE, 120),
// 			},
// 			nodeMetric: &metriccache.NodeResourceMetric{
// 				MemoryUsed: metriccache.MemoryMetric{
// 					MemoryWithoutCache: resource.MustParse("115G"),
// 				},
// 			},
// 			podMetrics: []*metriccache.PodResourceMetric{
// 				createPodResourceMetric("test_lsr_pod", "40G"),
// 				createPodResourceMetric("test_ls_pod", "30G"),
// 				createPodResourceMetric("test_noqos_pod", "10G"),
// 				createPodResourceMetric("test_be_pod_priority100_1", "5G"),  // evict
// 				createPodResourceMetric("test_be_pod_priority100_2", "20G"), // evict
// 				createPodResourceMetric("test_be_pod_priority120", "10G"),   // evict
// 			},
// 			thresholdConfig: &v1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: pointer.Int64Ptr(50)}, // >60G
// 			expectEvictPods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_be_pod_priority100_2", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority100_1", extension.QoSBE, 100),
// 				createMemoryEvictTestPod("test_be_pod_priority120", extension.QoSBE, 120),
// 			},
// 			expectNotEvictPods: []*corev1.Pod{
// 				createMemoryEvictTestPod("test_lsr_pod", extension.QoSLSR, 1000),
// 				createMemoryEvictTestPod("test_ls_pod", extension.QoSLS, 500),
// 				createMemoryEvictTestPod("test_noqos_pod", extension.QoSNone, 100),
// 			},
// 		},
// 	}
//
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			ctl := gomock.NewController(t)
// 			defer ctl.Finish()
//
// 			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
// 			mockStatesInformer.EXPECT().GetAllPods().Return(getPodMetas(tt.pods)).AnyTimes()
// 			mockStatesInformer.EXPECT().GetNode().Return(tt.node).AnyTimes()
//
// 			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
// 			mockNodeQueryResult := metriccache.NodeResourceQueryResult{Metric: tt.nodeMetric}
// 			mockMetricCache.EXPECT().GetNodeResourceMetric(gomock.Any()).Return(mockNodeQueryResult).AnyTimes()
// 			for _, podMetric := range tt.podMetrics {
// 				mockPodQueryResult := metriccache.PodResourceQueryResult{Metric: podMetric}
// 				mockMetricCache.EXPECT().GetPodResourceMetric(&podMetric.PodUID, gomock.Any()).Return(mockPodQueryResult).AnyTimes()
// 			}
//
// 			fakeRecorder := &FakeRecorder{}
// 			client := fake.NewSimpleClientset()
// 			resMgr := &resmanager{statesInformer: mockStatesInformer, eventRecorder: fakeRecorder, metricCache: mockMetricCache, kubeClient: client, nodeSLO: getNodeSLOByThreshold(tt.thresholdConfig), config: NewDefaultConfig()}
// 			stop := make(chan struct{})
// 			defer func() { stop <- struct{}{} }()
//
// 			runtime.DockerHandler = handler.NewFakeRuntimeHandler()
//
// 			var containers []*critesting.FakeContainer
// 			for _, pod := range tt.pods {
// 				_, err := client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
// 				assert.NoError(t, err, "createPod ERROR!")
// 				for _, containerStatus := range pod.Status.ContainerStatuses {
// 					_, containerId, _ := util.ParseContainerId(containerStatus.ContainerID)
// 					fakeContainer := &critesting.FakeContainer{
// 						SandboxID:       string(pod.UID),
// 						ContainerStatus: v1alpha2.ContainerStatus{Id: containerId},
// 					}
// 					containers = append(containers, fakeContainer)
// 				}
// 			}
// 			runtime.DockerHandler.(*handler.FakeRuntimeHandler).SetFakeContainers(containers)
//
// 			memoryEvictor := NewMemoryEvictor(resMgr)
// 			memoryEvictor.lastEvictTime = time.Now().Add(-30 * time.Second)
// 			memoryEvictor.memoryEvict()
//
// 			for _, pod := range tt.expectEvictPods {
// 				getEvictObject, err := client.Tracker().Get(podsResource, pod.Namespace, pod.Name)
// 				assert.NotNil(t, getEvictObject, "evictPod Fail", err)
// 			}
//
// 			for _, pod := range tt.expectNotEvictPods {
// 				getObject, _ := client.Tracker().Get(podsResource, pod.Namespace, pod.Name)
// 				assert.IsType(t, &corev1.Pod{}, getObject, "no need evict", pod.Name)
// 				// gotPod, err := client.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
// 				// assert.NotNil(t, gotPod, "no need evict!", err)
// 			}
// 		})
// 	}
// }
//
// func createMemoryEvictTestPod(name string, qosClass extension.QoSClass, priority int32) *corev1.Pod {
// 	return &corev1.Pod{
// 		TypeMeta: metav1.TypeMeta{Kind: "Pod"},
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name: name,
// 			UID:  types.UID(name),
// 			Annotations: map[string]string{
// 				extension.LabelPodQoS: string(qosClass),
// 			},
// 		},
// 		Spec: corev1.PodSpec{
// 			Containers: []corev1.Container{
// 				{
// 					Name: fmt.Sprintf("%s_%s", name, "main"),
// 				},
// 			},
// 			Priority: &priority,
// 		},
// 		Status: corev1.PodStatus{
// 			ContainerStatuses: []corev1.ContainerStatus{
// 				{
// 					Name:        fmt.Sprintf("%s_%s", name, "main"),
// 					ContainerID: fmt.Sprintf("docker://%s_%s", name, "main"),
// 					State: corev1.ContainerState{
// 						Running: &corev1.ContainerStateRunning{StartedAt: metav1.Time{Time: time.Now()}},
// 					},
// 				},
// 			},
// 		},
// 	}
// }
//
// func createPodResourceMetric(podUID string, memoryUsage string) *metriccache.PodResourceMetric {
// 	return &metriccache.PodResourceMetric{
// 		PodUID:     podUID,
// 		MemoryUsed: metriccache.MemoryMetric{MemoryWithoutCache: resource.MustParse(memoryUsage)},
// 	}
// }
