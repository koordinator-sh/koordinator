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
	"strconv"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
)

var _ = SIGDescribe("BECPUSuppress", func() {
	f := framework.NewDefaultFramework("becpusuppress")

	framework.KoordinatorDescribe("BECPUSuppress [koordlet]", func() {
		framework.ConformanceIt("should throttle cpu.cfs_quota_us on BE pod when BECPUSuppress is enabled", func() {
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

			configStr := `{"cpuSuppressThresholdPercent": 1, "cpuSuppressPolicy": "cfsQuota", "enable": true}`
			cm.Data[configuration.ResourceThresholdConfigKey] = configStr
			_, err = c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
			framework.ExpectNoError(err)

			// Create BE pod
			podName := "be-pod-suppress-" + framework.RandomSuffix()
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

			// Poll for cgroup cpu.cfs_quota_us
			gomega.Eventually(func() bool {
				cmd := `path=$(cat /proc/self/cgroup | grep ':cpu,' | head -1 | cut -d: -f3); if [ -z "$path" ]; then echo NOT_SUPPORTED; exit 0; fi; if [ ! -f "/sys/fs/cgroup/cpu$path/cpu.cfs_quota_us" ]; then echo NOT_SUPPORTED; exit 0; fi; cat /sys/fs/cgroup/cpu$path/cpu.cfs_quota_us`
				out, err := f.ExecCommandInContainerWithFullOutput(podName, "busybox", "/bin/sh", "-c", cmd)
				if err != nil {
					return false
				}
				outStr := strings.TrimSpace(out)
				if outStr == "NOT_SUPPORTED" {
					ginkgo.Skip("cgroup not supported or not found in testing environment")
				}
				quota, err := strconv.Atoi(outStr)
				if err != nil {
					return false
				}
				return quota > 0 && quota != -1
			}, 90, 5).Should(gomega.BeTrue())
		})

		framework.ConformanceIt("should restore cpu.cfs_quota_us to unlimited after BECPUSuppress is disabled", func() {
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

			configStr := `{"cpuSuppressThresholdPercent": 1, "cpuSuppressPolicy": "cfsQuota", "enable": true}`
			cm.Data[configuration.ResourceThresholdConfigKey] = configStr
			_, err = c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
			framework.ExpectNoError(err)

			// Create BE pod
			podName := "be-pod-suppress-restore-" + framework.RandomSuffix()
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

			// Poll for cgroup cpu.cfs_quota_us to be suppressed
			gomega.Eventually(func() bool {
				cmd := `path=$(cat /proc/self/cgroup | grep ':cpu,' | head -1 | cut -d: -f3); if [ -z "$path" ]; then echo NOT_SUPPORTED; exit 0; fi; if [ ! -f "/sys/fs/cgroup/cpu$path/cpu.cfs_quota_us" ]; then echo NOT_SUPPORTED; exit 0; fi; cat /sys/fs/cgroup/cpu$path/cpu.cfs_quota_us`
				out, err := f.ExecCommandInContainerWithFullOutput(podName, "busybox", "/bin/sh", "-c", cmd)
				if err != nil {
					return false
				}
				outStr := strings.TrimSpace(out)
				if outStr == "NOT_SUPPORTED" {
					ginkgo.Skip("cgroup not supported or not found in testing environment")
				}
				quota, err := strconv.Atoi(outStr)
				if err != nil {
					return false
				}
				return quota > 0 && quota != -1
			}, 90, 5).Should(gomega.BeTrue())

			// Disable suppression
			cmUpdate, err := c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Get(context.TODO(), "slo-controller-config", metav1.GetOptions{})
			framework.ExpectNoError(err)
			cmUpdate.Data[configuration.ResourceThresholdConfigKey] = `{"enable": false}`
			_, err = c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Update(context.TODO(), cmUpdate, metav1.UpdateOptions{})
			framework.ExpectNoError(err)

			// Poll for cgroup cpu.cfs_quota_us to be restored to -1
			gomega.Eventually(func() bool {
				cmd := `path=$(cat /proc/self/cgroup | grep ':cpu,' | head -1 | cut -d: -f3); if [ -z "$path" ]; then echo NOT_SUPPORTED; exit 0; fi; if [ ! -f "/sys/fs/cgroup/cpu$path/cpu.cfs_quota_us" ]; then echo NOT_SUPPORTED; exit 0; fi; cat /sys/fs/cgroup/cpu$path/cpu.cfs_quota_us`
				out, err := f.ExecCommandInContainerWithFullOutput(podName, "busybox", "/bin/sh", "-c", cmd)
				if err != nil {
					return false
				}
				outStr := strings.TrimSpace(out)
				if outStr == "NOT_SUPPORTED" {
					ginkgo.Skip("cgroup not supported or not found in testing environment")
				}
				quota, err := strconv.Atoi(outStr)
				if err != nil {
					return false
				}
				return quota == -1
			}, 90, 5).Should(gomega.BeTrue())
		})
	})
})
