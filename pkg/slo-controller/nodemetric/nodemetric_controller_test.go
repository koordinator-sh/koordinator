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

package nodemetric

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
)

var _ = Describe("NodeMetric Controller", func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		nodeName = "test-node"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When adding a new node", func() {
		It("Should get the corresponding NodeMetric resource successfully", func() {
			By("By creating a new node")
			ctx := context.Background()
			node := newNodeForTest(nodeName)

			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			key := types.NamespacedName{Name: node.Name}
			createdNodeMetric := &slov1alpha1.NodeMetric{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, createdNodeMetric)
				if err != nil {
					klog.Errorf("failed to get the corresponding NodeMetric, error: %v", err)
				}
				return err == nil
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("When node does not exist anymore", func() {
		It("Should delete the corresponding NodeMetric resource", func() {
			ctx := context.Background()
			node := newNodeForTest(nodeName)

			key := types.NamespacedName{Name: node.Name}
			createdNodeMetric := &slov1alpha1.NodeMetric{}

			Expect(k8sClient.Get(ctx, key, node)).Should(Succeed())
			Expect(k8sClient.Get(ctx, key, createdNodeMetric)).Should(Succeed())

			By("By deleting the node")

			Expect(k8sClient.Delete(ctx, node)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, createdNodeMetric)
				if err != nil && !errors.IsNotFound(err) {
					klog.Errorf("failed to get the deleting NodeMetric, error: %v", err)
				}
				return errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("Update NodeMetric through ConfigMap", func() {
		It("Should update NodeMetricCollectPolicy", func() {
			ctx := context.Background()
			node := newNodeForTest(nodeName)

			key := types.NamespacedName{Name: node.Name}
			createdNodeMetric := &slov1alpha1.NodeMetric{}

			By(fmt.Sprintf("create node %s", key))
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, createdNodeMetric)
				if err != nil {
					klog.Errorf("failed to get the corresponding NodeMetric, error: %v", err)
				}
				return err == nil
			}, timeout, interval).Should(BeTrue())

			ns := &corev1.Namespace{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: config.ConfigNameSpace}, ns)
			if errors.IsNotFound(err) {
				By(fmt.Sprintf("create config namespace %s", config.ConfigNameSpace))
				ns.Name = config.ConfigNameSpace
				Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
			} else {
				Fail(fmt.Sprintf("Failed to get namespace %s, err: %v", config.ConfigNameSpace, err))
			}

			By("create slo-controller-config with nodeMetricCollectPolicyCfg")
			policyConfig := &config.ColocationCfg{
				ColocationStrategy: config.ColocationStrategy{
					Enable:                         pointer.Bool(true),
					MetricAggregateDurationSeconds: pointer.Int64(60),
					MetricReportIntervalSeconds:    pointer.Int64(180),
				},
			}
			data, err := json.Marshal(policyConfig)
			Expect(err).Should(Succeed())

			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: config.ConfigNameSpace,
					Name:      config.SLOCtrlConfigMap,
				},
				Data: map[string]string{
					config.ColocationConfigKey: string(data),
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: config.SLOCtrlConfigMap, Namespace: config.ConfigNameSpace}, configMap)).Should(Succeed())

			By("update nodeMetric status to trigger update nodeMetricSpec")
			Eventually(func() bool {
				key := types.NamespacedName{Name: node.Name}
				nodeMetric := &slov1alpha1.NodeMetric{}
				err := k8sClient.Get(ctx, key, nodeMetric)
				if err != nil {
					klog.Errorf("failed to get the corresponding NodeMetric, error: %v", err)
					return false
				}
				if nodeMetric.Spec.CollectPolicy != nil &&
					nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds != nil &&
					*nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds == 180 {
					return true
				}

				nodeMetric.Status.UpdateTime = &metav1.Time{
					Time: time.Now(),
				}
				err = k8sClient.Update(ctx, nodeMetric)
				if err != nil {
					klog.Errorf("failed to update nodeMetric, err: %v", err)
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})
	})
})

func newNodeForTest(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}
