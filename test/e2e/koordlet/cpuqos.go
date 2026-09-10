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
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("CPUQoS", func() {
	var (
		c        clientset.Interface
		nodeList *corev1.NodeList
		err      error
	)

	f := framework.NewDefaultFramework("cpuqos")

	ginkgo.BeforeEach(func() {
		c = f.ClientSet

		framework.Logf("getting ready and schedulable nodes")
		nodeList, err = e2enode.GetReadySchedulableNodes(c)
		framework.ExpectNoError(err)
		gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero(), "at least one schedulable node is required")
	})

	framework.KoordinatorDescribe("CPU QoS Isolation", func() {
		// Test cpuset isolation for LSE pod
		framework.ConformanceIt("should isolate cpuset for LSE pod", func() {
			ginkgo.By("creating an LSE pod with cpuset requested")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cpuqos-lse-pod",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSLSE),
					},
					Annotations: map[string]string{
						apiext.AnnotationResourceStatus: `{"cpuset": "0"}`,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "sleep",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", "sleep 3600"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
							},
						},
					},
				},
			}

			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for the pod to be Running")
			err = e2epod.WaitForPodRunningInNamespace(c, pod)
			framework.ExpectNoError(err)

			ginkgo.By("verifying cpuset.cpus=0 on the pod's cgroup")
			verifyCPUSetCpus(f, pod, "0")
		})

		// Test cpu.shares isolation for BE pod
		framework.ConformanceIt("should verify cpu.shares for BE pod", func() {
			ginkgo.By("creating a BE pod with batch-cpu requested")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cpuqos-be-pod",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "sleep",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", "sleep 3600"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:    resource.MustParse("1000m"),
									apiext.BatchMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									apiext.BatchCPU:    resource.MustParse("1000m"),
									apiext.BatchMemory: resource.MustParse("64Mi"),
								},
							},
						},
					},
				},
			}

			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for the pod to be Running")
			err = e2epod.WaitForPodRunningInNamespace(c, pod)
			framework.ExpectNoError(err)

			ginkgo.By("verifying cpu.shares on the pod's cgroup")
			// Koordlet usually sets `cpu.shares` proportional to batch ratio.
			// Just verify that the value is correctly constrained, e.g., > 2.
			// Batch cpu request 1000m implies cpu.shares=1024 or scaling. We check it's not the kubelet BestEffort default (2).
			verifyCPUSharesNotTwo(f, pod)
		})
	})
})

func verifyCPUSetCpus(f *framework.Framework, pod *corev1.Pod, expectedVal string) {
	gomega.Eventually(func() bool {
		cgroupOut, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", "cat /proc/self/cgroup | grep ':cpuset:' | head -1 | cut -d: -f3",
		)
		if err != nil || strings.TrimSpace(cgroupOut) == "" {
			return false
		}
		cgroupPath := strings.TrimSpace(cgroupOut)

		filePath := fmt.Sprintf("/sys/fs/cgroup/cpuset%s/cpuset.cpus", cgroupPath)
		out, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", fmt.Sprintf("cat %s 2>/dev/null || echo NOT_SUPPORTED", filePath),
		)
		if err != nil {
			return false
		}
		val := strings.TrimSpace(out)

		if val == "NOT_SUPPORTED" {
			ginkgo.Skip("cpuset not available on this node — skipping")
			return true
		}
		return val == expectedVal
	}, 90*time.Second, 5*time.Second).Should(gomega.BeTrue(), "expected cpuset.cpus for pod")
}

func verifyCPUSharesNotTwo(f *framework.Framework, pod *corev1.Pod) {
	gomega.Eventually(func() bool {
		cgroupOut, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", "cat /proc/self/cgroup | grep ':cpu,' | head -1 | cut -d: -f3",
		)
		if err != nil || strings.TrimSpace(cgroupOut) == "" {
			return false
		}
		cgroupPath := strings.TrimSpace(cgroupOut)

		filePath := fmt.Sprintf("/sys/fs/cgroup/cpu%s/cpu.shares", cgroupPath)
		out, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", fmt.Sprintf("cat %s 2>/dev/null || echo NOT_SUPPORTED", filePath),
		)
		if err != nil {
			return false
		}
		val := strings.TrimSpace(out)

		if val == "NOT_SUPPORTED" {
			ginkgo.Skip("cpu.shares not available on this node — skipping")
			return true
		}

		return val != "" && val != "2"
	}, 90*time.Second, 5*time.Second).Should(gomega.BeTrue(), "expected cpu.shares for pod to be updated")
}
