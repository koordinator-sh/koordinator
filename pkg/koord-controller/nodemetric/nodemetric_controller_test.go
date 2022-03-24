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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
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
})

func newNodeForTest(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}
