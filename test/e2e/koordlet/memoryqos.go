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
	// memoryQoSTimeout is how long to wait for koordlet to apply memory QoS cgroup values.
	memoryQoSTimeout = 90 * time.Second
	memoryQoSPoll    = 5 * time.Second

	// wmarkRatioValue is the memory.wmark_ratio we configure (0–100).
	// koordlet writes this directly to the cgroup file.
	wmarkRatioValue = 95

	// wmarkMinAdjBE is the memory.wmark_min_adj for BE pods (positive = raises watermark).
	wmarkMinAdjBE = 50
)

// memoryQoSEnabledConfigData enables MemoryQoS for LS and BE pods with specific watermark settings.
func memoryQoSEnabledConfigData() string {
	return fmt.Sprintf(`{
  "lsClass": {
    "memoryQOS": {
      "enable": true,
      "wmarkRatio": %d
    }
  },
  "beClass": {
    "memoryQOS": {
      "enable": true,
      "wmarkRatio": %d,
      "wmarkMinAdj": %d
    }
  }
}`, wmarkRatioValue, wmarkRatioValue, wmarkMinAdjBE)
}

var _ = SIGDescribe("MemoryQoS", func() {
	var (
		c              clientset.Interface
		nodeList       *corev1.NodeList
		err            error
		koordNamespace string
		sloConfigName  string
	)

	f := framework.NewDefaultFramework("memoryqos")

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

	framework.KoordinatorDescribe("MemoryQoS Watermarks", func() {
		// Test 1: LS pod gets memory.wmark_ratio set by koordlet
		framework.ConformanceIt("should set memory.wmark_ratio on an LS pod when MemoryQoS is enabled", func() {
			ginkgo.By("enabling MemoryQoS in slo-controller-config")
			rollback := ensureMemoryQoSEnabled(f, c, koordNamespace, sloConfigName)
			if rollback != nil {
				defer rollback()
			}

			ginkgo.By("creating an LS pod")
			pod := newMemoryQoSTestPod(f.Namespace.Name, "memqos-ls-pod", apiext.QoSLS)
			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for the pod to be Running")
			waitForPodRunning(f, c, pod)

			ginkgo.By(fmt.Sprintf("verifying memory.wmark_ratio=%d on the pod's cgroup", wmarkRatioValue))
			verifyMemoryCgroupValue(f, pod, "memory.wmark_ratio", int64(wmarkRatioValue))
		})

		// Test 2: BE pod gets memory.wmark_min_adj set by koordlet
		framework.ConformanceIt("should set memory.wmark_min_adj on a BE pod when MemoryQoS is enabled", func() {
			ginkgo.By("enabling MemoryQoS in slo-controller-config")
			rollback := ensureMemoryQoSEnabled(f, c, koordNamespace, sloConfigName)
			if rollback != nil {
				defer rollback()
			}

			ginkgo.By("creating a BE pod")
			pod := newMemoryQoSTestPod(f.Namespace.Name, "memqos-be-pod", apiext.QoSBE)
			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for the pod to be Running")
			waitForPodRunning(f, c, pod)

			ginkgo.By(fmt.Sprintf("verifying memory.wmark_min_adj=%d on the BE pod's cgroup", wmarkMinAdjBE))
			verifyMemoryCgroupValue(f, pod, "memory.wmark_min_adj", int64(wmarkMinAdjBE))
		})

		// Test 3: Disabling MemoryQoS resets memory.wmark_ratio to 0
		framework.ConformanceIt("should reset memory.wmark_ratio to 0 after MemoryQoS is disabled", func() {
			ginkgo.By("enabling MemoryQoS in slo-controller-config")
			rollback := ensureMemoryQoSEnabled(f, c, koordNamespace, sloConfigName)
			if rollback != nil {
				defer rollback()
			}

			ginkgo.By("creating an LS pod")
			pod := newMemoryQoSTestPod(f.Namespace.Name, "memqos-reset-pod", apiext.QoSLS)
			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for pod Running and watermark applied")
			waitForPodRunning(f, c, pod)
			verifyMemoryCgroupValue(f, pod, "memory.wmark_ratio", int64(wmarkRatioValue))

			ginkgo.By("disabling MemoryQoS in slo-controller-config")
			disableMemoryQoS(f, c, koordNamespace, sloConfigName)

			ginkgo.By("verifying memory.wmark_ratio resets to 0")
			verifyMemoryCgroupValue(f, pod, "memory.wmark_ratio", 0)
		})
	})
})

// newMemoryQoSTestPod creates a pod with a memory limit so koordlet can compute memory.min.
func newMemoryQoSTestPod(namespace, name string, qos apiext.QoSClass) *corev1.Pod {
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
					Name:    "sleep",
					Image:   "busybox",
					Command: []string{"/bin/sh", "-c", "sleep 3600"},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("50m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			},
		},
	}
}

// verifyMemoryCgroupValue execs into the pod to check that a given memory cgroup file
// holds the expected value. Polls for memoryQoSTimeout.
func verifyMemoryCgroupValue(f *framework.Framework, pod *corev1.Pod, filename string, expectedVal int64) {
	gomega.Eventually(func() bool {
		// Get cgroup path from /proc/self/cgroup (memory subsystem).
		cgroupOut, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", "cat /proc/self/cgroup | grep ':memory:' | head -1 | cut -d: -f3",
		)
		if err != nil || strings.TrimSpace(cgroupOut) == "" {
			// Fall back to cpu subsystem cgroup path if memory line not present.
			cgroupOut, _, err = f.ExecCommandInContainerWithFullOutput(
				pod.Name,
				pod.Spec.Containers[0].Name,
				"/bin/sh", "-c", "cat /proc/self/cgroup | grep ':cpu,' | head -1 | cut -d: -f3",
			)
			if err != nil || strings.TrimSpace(cgroupOut) == "" {
				framework.Logf("could not read cgroup path from pod %s: %v", pod.Name, err)
				return false
			}
		}
		cgroupPath := strings.TrimSpace(cgroupOut)

		filePath := fmt.Sprintf("/sys/fs/cgroup/memory%s/%s", cgroupPath, filename)
		out, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", fmt.Sprintf("cat %s 2>/dev/null || echo NOT_SUPPORTED", filePath),
		)
		if err != nil {
			framework.Logf("error reading %s from pod %s: %v", filename, pod.Name, err)
			return false
		}
		val := strings.TrimSpace(out)
		framework.Logf("pod %s %s=%q (expected %d)", pod.Name, filename, val, expectedVal)

		if val == "NOT_SUPPORTED" {
			ginkgo.Skip(fmt.Sprintf("%s not available on this node — skipping", filename))
			return true
		}
		got, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			framework.Logf("could not parse %s value %q: %v", filename, val, err)
			return false
		}
		return got == expectedVal
	}, memoryQoSTimeout, memoryQoSPoll).Should(gomega.BeTrue(),
		fmt.Sprintf("expected %s=%d for pod %s", filename, expectedVal, pod.Name))
}

// ensureMemoryQoSEnabled patches slo-controller-config to enable MemoryQoS.
func ensureMemoryQoSEnabled(f *framework.Framework, c clientset.Interface, koordNamespace, sloConfigName string) func() {
	configMap, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
	if err != nil {
		framework.Logf("slo-controller-config not found, skipping MemoryQoS config patch: %v", err)
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
	newConfigMap.Data[configuration.ResourceQOSConfigKey] = memoryQoSEnabledConfigData()
	_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err, "failed to update slo-controller-config to enable MemoryQoS")
	framework.Logf("enabled MemoryQoS in slo-controller-config")

	return func() {
		cm, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
		if err != nil {
			framework.Logf("error getting configmap for rollback: %v", err)
			return
		}
		rb := cm.DeepCopy()
		rb.Data = oldData
		_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), rb, metav1.UpdateOptions{})
		if err != nil {
			framework.Logf("error rolling back slo-controller-config: %v", err)
		} else {
			framework.Logf("rolled back slo-controller-config after MemoryQoS test")
		}
	}
}

// disableMemoryQoS patches the slo-controller-config to disable MemoryQoS.
func disableMemoryQoS(f *framework.Framework, c clientset.Interface, koordNamespace, sloConfigName string) {
	const disabledData = `{
  "lsClass": {
    "memoryQOS": {
      "enable": false
    }
  },
  "beClass": {
    "memoryQOS": {
      "enable": false
    }
  }
}`
	configMap, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
	framework.ExpectNoError(err)
	newConfigMap := configMap.DeepCopy()
	if newConfigMap.Data == nil {
		newConfigMap.Data = map[string]string{}
	}
	newConfigMap.Data[configuration.ResourceQOSConfigKey] = disabledData
	_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err)
	framework.Logf("disabled MemoryQoS in slo-controller-config")
}
