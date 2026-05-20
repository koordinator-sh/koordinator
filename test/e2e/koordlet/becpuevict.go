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

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
)

var _ = SIGDescribe("BECPUEvict", func() {
	f := framework.NewDefaultFramework("becpuevict")

	framework.KoordinatorDescribe("BECPUEvict [koordlet]", func() {
		framework.ConformanceIt("should evict a CPU-hungry BE pod when BECPUEvict is enabled", func() {
			c := f.ClientSet

			// Patch config
			cm, err := c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Get(context.TODO(), "slo-controller-config", metav1.GetOptions{})
			framework.ExpectNoError(err, "failed to get slo-controller-config")
			oldData := cm.DeepCopy().Data
			defer func() {
				cmToRestore, err := c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Get(context.TODO(), "slo-controller-config", metav1.GetOptions{})
				framework.ExpectNoError(err)
				cmToRestore.Data = oldData
				_, err = c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Update(context.TODO(), cmToRestore, metav1.UpdateOptions{})
				framework.ExpectNoError(err)
			}()

			if cm.Data == nil {
				cm.Data = make(map[string]string)
			}

			configStr := `{"cpuEvictBESatisfactionPercent": 90, "cpuEvictTimeWindowSeconds": 60, "enable": true}`
			cm.Data[configuration.ResourceThresholdConfigKey] = configStr
			_, err = c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
			framework.ExpectNoError(err)

			// Create BE pod
			podName := "be-pod-evict-" + framework.RandomSuffix()
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "busybox",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", "while true; do :; done"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("500m"),
								},
							},
						},
					},
				},
			}

			_, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), podName, metav1.DeleteOptions{})
			}()

			waitForPodRunning(f, c, pod)

			// Poll for eviction (phase=Failed, reason=Evicted OR deleted)
			gomega.Eventually(func() bool {
				p, err := c.CoreV1().Pods(f.Namespace.Name).Get(context.TODO(), podName, metav1.GetOptions{})
				if err != nil {
					if apierrors.IsNotFound(err) {
						return true
					}
					return false
				}
				if p.Status.Phase == corev1.PodFailed && p.Status.Reason == "Evicted" {
					return true
				}
				return false
			}, 180, 10).Should(gomega.BeTrue())
		})

		framework.ConformanceIt("should NOT evict a BE pod when BECPUEvict is disabled", func() {
			c := f.ClientSet

			// Patch config
			cm, err := c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Get(context.TODO(), "slo-controller-config", metav1.GetOptions{})
			framework.ExpectNoError(err, "failed to get slo-controller-config")
			oldData := cm.DeepCopy().Data
			defer func() {
				cmToRestore, err := c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Get(context.TODO(), "slo-controller-config", metav1.GetOptions{})
				framework.ExpectNoError(err)
				cmToRestore.Data = oldData
				_, err = c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Update(context.TODO(), cmToRestore, metav1.UpdateOptions{})
				framework.ExpectNoError(err)
			}()

			if cm.Data == nil {
				cm.Data = make(map[string]string)
			}

			configStr := `{"enable": false}`
			cm.Data[configuration.ResourceThresholdConfigKey] = configStr
			_, err = c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
			framework.ExpectNoError(err)

			// Create BE pod
			podName := "be-pod-no-evict-" + framework.RandomSuffix()
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "busybox",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", "while true; do :; done"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("500m"),
								},
							},
						},
					},
				},
			}

			_, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), podName, metav1.DeleteOptions{})
			}()

			waitForPodRunning(f, c, pod)

			// Poll consistently to ensure pod is NOT evicted
			gomega.Consistently(func() bool {
				p, err := c.CoreV1().Pods(f.Namespace.Name).Get(context.TODO(), podName, metav1.GetOptions{})
				if err != nil {
					return false
				}
				return p.Status.Phase == corev1.PodRunning
			}, 90, 10).Should(gomega.BeTrue())
		})
	})
})
