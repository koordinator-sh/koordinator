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
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
)

const (
	// cpuBurstTimeout is how long to wait for koordlet to apply the cpu.cfs_burst_us value.
	cpuBurstTimeout = 90 * time.Second
	cpuBurstPoll    = 5 * time.Second

	// cpuLimitMillis is the CPU limit used for the test pod (200m).
	cpuLimitMillis = 200

	// cpuBurstPercent is the burst percent set in the config (1000% = 10x).
	// cpu.cfs_burst_us = limit_cores * burstPercent/100 * cfs_period_us
	// = 0.2 * 10 * 100000 = 200000 µs
	cpuBurstPercentValue = 1000

	// cfsPeriodUs is the standard CFS period in microseconds.
	cfsPeriodUs = 100000
)

// expectedCFSBurstUs calculates the expected cpu.cfs_burst_us value.
// Formula from koordlet: limit_cores * (burstPercent/100) * cfs_period_us
func expectedCFSBurstUs(cpuMilliLimit int64, burstPercent int64) int64 {
	limitCores := float64(cpuMilliLimit) / 1000.0
	return int64(limitCores * float64(burstPercent) / 100.0 * cfsPeriodUs)
}

var _ = SIGDescribe("CPUBurst", func() {
	var (
		c              clientset.Interface
		nodeList       *corev1.NodeList
		err            error
		koordNamespace string
		sloConfigName  string
	)

	f := framework.NewDefaultFramework("cpuburst")

	ginkgo.BeforeEach(func() {
		c = f.ClientSet
		koordNamespace = framework.TestContext.KoordinatorComponentNamespace
		sloConfigName = framework.TestContext.SLOCtrlConfigMap

		framework.Logf("getting ready and schedulable nodes")
		nodeList, err = e2enode.GetReadySchedulableNodes(c)
		framework.ExpectNoError(err)
		gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero(),
			"at least one schedulable node is required")
	})

	framework.KoordinatorDescribe("CPUBurst CFSBurstUs", func() {
		// Test 1: LS pod gets cpu.cfs_burst_us set when CPUBurst is enabled
		framework.ConformanceIt("should set cpu.cfs_burst_us on LS pod containers when CPUBurst is enabled", func() {
			ginkgo.By("enabling CPUBurst in slo-controller-config")
			rollback := ensureCPUBurstEnabled(f, c, koordNamespace, sloConfigName)
			if rollback != nil {
				defer rollback()
			}

			ginkgo.By("creating an LS pod with a known CPU limit")
			pod := newCPUBurstTestPod(f.Namespace.Name, "cpuburst-ls-pod", apiext.QoSLS)
			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for the pod to be Running")
			waitForPodRunning(f, c, pod)

			// expected: 0.2 cores * (1000/100) * 100000µs = 200000µs
			expected := expectedCFSBurstUs(cpuLimitMillis, cpuBurstPercentValue)
			ginkgo.By(fmt.Sprintf("verifying cpu.cfs_burst_us=%d on the container cgroup", expected))
			verifyCFSBurstUs(f, pod, expected)
		})

		// Test 2: Disabling CPUBurst resets cpu.cfs_burst_us to 0
		framework.ConformanceIt("should reset cpu.cfs_burst_us to 0 after CPUBurst is disabled", func() {
			ginkgo.By("enabling CPUBurst in slo-controller-config")
			rollback := ensureCPUBurstEnabled(f, c, koordNamespace, sloConfigName)
			if rollback != nil {
				defer rollback()
			}

			ginkgo.By("creating an LS pod")
			pod := newCPUBurstTestPod(f.Namespace.Name, "cpuburst-reset-pod", apiext.QoSLS)
			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for pod Running")
			waitForPodRunning(f, c, pod)

			expected := expectedCFSBurstUs(cpuLimitMillis, cpuBurstPercentValue)
			ginkgo.By(fmt.Sprintf("verifying initial cpu.cfs_burst_us=%d", expected))
			verifyCFSBurstUs(f, pod, expected)

			ginkgo.By("disabling CPUBurst in slo-controller-config")
			disableCPUBurst(f, c, koordNamespace, sloConfigName)

			ginkgo.By("verifying cpu.cfs_burst_us resets to 0")
			verifyCFSBurstUs(f, pod, 0)
		})
	})
})

// newCPUBurstTestPod creates a pod with a fixed CPU limit so we can calculate the expected cfs_burst_us.
func newCPUBurstTestPod(namespace, name string, qos apiext.QoSClass) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qos),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "stress",
					Image:   "busybox",
					Command: []string{"/bin/sh", "-c", "sleep 3600"},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
						Limits: corev1.ResourceList{
							// Keep this in sync with cpuLimitMillis constant above.
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			},
		},
	}
}

// verifyCFSBurstUs execs into the pod's first container, finds its cgroup, and checks
// that cpu.cfs_burst_us matches expectedUs. Polls for cpuBurstTimeout.
func verifyCFSBurstUs(f *framework.Framework, pod *corev1.Pod, expectedUs int64) {
	gomega.Eventually(func() bool {
		// Get the cgroup path from /proc/self/cgroup.
		cgroupOut, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", "cat /proc/self/cgroup | grep ':cpu,' | head -1 | cut -d: -f3",
		)
		if err != nil || strings.TrimSpace(cgroupOut) == "" {
			framework.Logf("could not read cgroup path from pod %s: %v", pod.Name, err)
			return false
		}
		cgroupPath := strings.TrimSpace(cgroupOut)

		// Read cpu.cfs_burst_us (cgroup v1 only).
		burstPath := fmt.Sprintf("/sys/fs/cgroup/cpu%s/cpu.cfs_burst_us", cgroupPath)
		burstOut, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", fmt.Sprintf("cat %s 2>/dev/null || echo NOT_SUPPORTED", burstPath),
		)
		if err != nil {
			framework.Logf("error reading cpu.cfs_burst_us from pod %s: %v", pod.Name, err)
			return false
		}
		val := strings.TrimSpace(burstOut)
		framework.Logf("pod %s cpu.cfs_burst_us=%q (expected %d)", pod.Name, val, expectedUs)

		if val == "NOT_SUPPORTED" {
			ginkgo.Skip("cpu.cfs_burst_us not available on this node (cgroup v2 or kernel < 5.14)")
			return true
		}

		got, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			framework.Logf("could not parse cpu.cfs_burst_us value %q: %v", val, err)
			return false
		}
		return got == expectedUs
	}, cpuBurstTimeout, cpuBurstPoll).Should(gomega.BeTrue(),
		fmt.Sprintf("expected cpu.cfs_burst_us=%d for pod %s", expectedUs, pod.Name))
}

// ensureCPUBurstEnabled patches slo-controller-config to enable CPUBurst with a 1000% burst ceiling.
// Returns a rollback function.
func ensureCPUBurstEnabled(f *framework.Framework, c clientset.Interface, koordNamespace, sloConfigName string) func() {
	cpuBurstEnabledData := fmt.Sprintf(`{
  "clsCFSQuotaBurstPercent": %d,
  "cpuBurstPercent": %d
}`, cpuBurstPercentValue, cpuBurstPercentValue)

	configMap, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
	if err != nil {
		framework.Logf("slo-controller-config not found, skipping CPUBurst config patch: %v", err)
		return nil
	}

	oldData := map[string]string{}
	if configMap.Data != nil {
		for k, v := range configMap.Data {
			oldData[k] = v
		}
	}

	newConfigMap := configMap.DeepCopy()
	if newConfigMap.Data == nil {
		newConfigMap.Data = map[string]string{}
	}
	newConfigMap.Data[configuration.CPUBurstConfigKey] = cpuBurstEnabledData
	_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err, "failed to update slo-controller-config to enable CPUBurst")
	framework.Logf("enabled CPUBurst in slo-controller-config (burstPercent=%d)", cpuBurstPercentValue)

	return func() {
		cm, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
		if err != nil {
			framework.Logf("error getting configmap for rollback: %v", err)
			return
		}
		rollback := cm.DeepCopy()
		rollback.Data = oldData
		_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), rollback, metav1.UpdateOptions{})
		if err != nil {
			framework.Logf("error rolling back slo-controller-config: %v", err)
		} else {
			framework.Logf("rolled back slo-controller-config after CPUBurst test")
		}
	}
}

// disableCPUBurst patches slo-controller-config to turn off CPUBurst.
func disableCPUBurst(f *framework.Framework, c clientset.Interface, koordNamespace, sloConfigName string) {
	const disabledData = `{
  "cpuBurstPercent": 0
}`
	configMap, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
	framework.ExpectNoError(err)
	newConfigMap := configMap.DeepCopy()
	if newConfigMap.Data == nil {
		newConfigMap.Data = map[string]string{}
	}
	newConfigMap.Data[configuration.CPUBurstConfigKey] = disabledData
	_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err)
	framework.Logf("disabled CPUBurst in slo-controller-config")
}
