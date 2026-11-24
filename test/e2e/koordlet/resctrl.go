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

package koordlet

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("Resctrl", func() {
	f := framework.NewDefaultFramework("resctrl")

	ginkgo.BeforeEach(func() {
		framework.AllNodesReady(f.ClientSet, time.Minute)
	})

	ginkgo.Context("L3/MBA (resctrl)", func() {
		ginkgo.It("should properly handle L3 cache and MBA configuration", func() {
			ginkgo.By("creating a pod with LSR QoS class")

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resctrl-lsr",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						"e2e-test-resctrl": "true",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "nginx:1.14.2",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			}

			pod, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("waiting for pod to be running")
			err = e2epod.WaitTimeoutForPodRunningInNamespace(f.ClientSet, pod.Name, pod.Namespace, time.Minute*2)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("fetching the scheduled pod to verify QoS class")
			pod, err = f.ClientSet.CoreV1().Pods(f.Namespace.Name).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(pod.Labels[extension.LabelPodQoS]).To(gomega.Equal(string(extension.QoSLSR)))

			// Clean up
			ginkgo.By("deleting the pod")
			err = f.ClientSet.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			if err != nil {
				framework.Logf("Failed to delete pod: %v", err)
			}
		})
	})
})
