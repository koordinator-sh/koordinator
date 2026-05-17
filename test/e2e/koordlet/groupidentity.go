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
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
)

const (
	// groupIdentityEnabledConfigData enables GroupIdentity (CPU BVT) for LS (+2) and BE (-1).
	groupIdentityEnabledConfigData = `{
  "lsClass": {
    "cpuQOS": {
      "enable": true,
      "groupIdentity": 2
    }
  },
  "beClass": {
    "cpuQOS": {
      "enable": true,
      "groupIdentity": -1
    }
  }
}`

	// groupIdentityTimeout is how long to wait for koordlet to reconcile BVT values.
	groupIdentityTimeout = 60 * time.Second
	groupIdentityPoll    = 3 * time.Second
)

var _ = SIGDescribe("GroupIdentity", func() {
	var (
		c           clientset.Interface
		koordClient koordinatorclientset.Interface
		nodeList    *corev1.NodeList
		err         error

		koordNamespace string
		sloConfigName  string
	)

	f := framework.NewDefaultFramework("groupidentity")

	ginkgo.BeforeEach(func() {
		c = f.ClientSet
		koordClient = f.KoordinatorClientSet
		koordNamespace = framework.TestContext.KoordinatorComponentNamespace
		sloConfigName = framework.TestContext.SLOCtrlConfigMap

		framework.Logf("getting ready and schedulable nodes")
		nodeList, err = e2enode.GetReadySchedulableNodes(c)
		framework.ExpectNoError(err)
		gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero(),
			"at least one schedulable node is required")
	})

	framework.KoordinatorDescribe("GroupIdentity BVT", func() {
		// Test 1: LS pod gets BVT value +2
		framework.ConformanceIt("should set cpu.bvt_warp_ns=2 for an LS pod when GroupIdentity is enabled", func() {
			ginkgo.By("enabling GroupIdentity in slo-controller-config")
			rollback := ensureGroupIdentityEnabled(f, c, koordClient, koordNamespace, sloConfigName)
			if rollback != nil {
				defer rollback()
			}

			ginkgo.By("creating an LS pod with koordinator QoS label")
			pod := newGroupIdentityTestPod(f.Namespace.Name, "gi-ls-pod", apiext.QoSLS)
			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for the pod to be Running")
			waitForPodRunning(f, c, pod)

			ginkgo.By("verifying that koordlet set cpu.bvt_warp_ns=2 on the pod's cgroup")
			verifyPodBVTValue(f, c, pod, 2)
		})

		// Test 2: BE pod gets BVT value -1
		framework.ConformanceIt("should set cpu.bvt_warp_ns=-1 for a BE pod when GroupIdentity is enabled", func() {
			ginkgo.By("enabling GroupIdentity in slo-controller-config")
			rollback := ensureGroupIdentityEnabled(f, c, koordClient, koordNamespace, sloConfigName)
			if rollback != nil {
				defer rollback()
			}

			ginkgo.By("creating a BE pod with koordinator QoS label")
			pod := newGroupIdentityTestPod(f.Namespace.Name, "gi-be-pod", apiext.QoSBE)
			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for the pod to be Running")
			waitForPodRunning(f, c, pod)

			ginkgo.By("verifying that koordlet set cpu.bvt_warp_ns=-1 on the pod's cgroup")
			verifyPodBVTValue(f, c, pod, -1)
		})

		// Test 3: Disabling GroupIdentity resets BVT to 0
		framework.ConformanceIt("should reset cpu.bvt_warp_ns to 0 after GroupIdentity is disabled", func() {
			ginkgo.By("enabling GroupIdentity in slo-controller-config")
			rollback := ensureGroupIdentityEnabled(f, c, koordClient, koordNamespace, sloConfigName)
			if rollback != nil {
				defer rollback()
			}

			ginkgo.By("creating an LS pod")
			pod := newGroupIdentityTestPod(f.Namespace.Name, "gi-reset-pod", apiext.QoSLS)
			pod, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			ginkgo.By("waiting for pod Running and BVT=2")
			waitForPodRunning(f, c, pod)
			verifyPodBVTValue(f, c, pod, 2)

			ginkgo.By("disabling GroupIdentity in slo-controller-config")
			disableGroupIdentity(f, c, koordClient, koordNamespace, sloConfigName)

			ginkgo.By("verifying that koordlet resets cpu.bvt_warp_ns to 0")
			verifyPodBVTValue(f, c, pod, 0)
		})
	})
})

// newGroupIdentityTestPod creates a minimal long-running pod with the given koordinator QoS label.
func newGroupIdentityTestPod(namespace, name string, qos apiext.QoSClass) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qos),
			},
		},
		Spec: corev1.PodSpec{
			// Use a node with koordlet running — no specific node selector needed
			// since all nodes should have koordlet via DaemonSet.
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "sleep",
					Image: "busybox",
					Command: []string{
						"/bin/sh", "-c", "sleep 3600",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
			},
		},
	}
}

// waitForPodRunning polls until the pod is in Running state.
func waitForPodRunning(f *framework.Framework, c clientset.Interface, pod *corev1.Pod) {
	gomega.Eventually(func() bool {
		p, err := c.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if err != nil {
			framework.Logf("error getting pod %s: %v", pod.Name, err)
			return false
		}
		_, podReady := k8spodutil.GetPodCondition(&p.Status, corev1.PodReady)
		if podReady == nil || podReady.Status != corev1.ConditionTrue {
			framework.Logf("pod %s not ready yet, phase=%s", pod.Name, p.Status.Phase)
			return false
		}
		return true
	}, 120*time.Second, 3*time.Second).Should(gomega.BeTrue(), "pod should become Running")
}

// verifyPodBVTValue exec's into the pod to read the cgroup's cpu.bvt_warp_ns file
// and asserts it equals the expected value. Retries for groupIdentityTimeout to
// allow koordlet's reconcile loop to run.
func verifyPodBVTValue(f *framework.Framework, c clientset.Interface, pod *corev1.Pod, expectedBVT int) {
	gomega.Eventually(func() bool {
		// Read /proc/self/cgroup to find the cgroup path of the container.
		cgroupOut, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", "cat /proc/self/cgroup | grep ':cpu,' | head -1 | cut -d: -f3",
		)
		if err != nil || strings.TrimSpace(cgroupOut) == "" {
			framework.Logf("could not get cgroup path from pod %s: %v", pod.Name, err)
			return false
		}
		cgroupPath := strings.TrimSpace(cgroupOut)
		framework.Logf("pod %s cgroup path: %s", pod.Name, cgroupPath)

		// Read cpu.bvt_warp_ns from the cgroup directory (cgroup v1: /sys/fs/cgroup/cpu/<path>).
		bvtPath := fmt.Sprintf("/sys/fs/cgroup/cpu%s/cpu.bvt_warp_ns", cgroupPath)
		bvtOut, _, err := f.ExecCommandInContainerWithFullOutput(
			pod.Name,
			pod.Spec.Containers[0].Name,
			"/bin/sh", "-c", fmt.Sprintf("cat %s 2>/dev/null || echo NOT_SUPPORTED", bvtPath),
		)
		if err != nil {
			framework.Logf("error reading bvt file from pod %s: %v", pod.Name, err)
			return false
		}
		bvtStr := strings.TrimSpace(bvtOut)
		framework.Logf("pod %s bvt value: %q (expected %d)", pod.Name, bvtStr, expectedBVT)

		if bvtStr == "NOT_SUPPORTED" {
			// cgroup v2 path or file doesn't exist — skip gracefully.
			ginkgo.Skip("cpu.bvt_warp_ns not available on this node (cgroup v2 or Anolis kernel not present)")
			return true
		}

		return bvtStr == fmt.Sprintf("%d", expectedBVT)
	}, groupIdentityTimeout, groupIdentityPoll).Should(gomega.BeTrue(),
		fmt.Sprintf("expected cpu.bvt_warp_ns=%d for pod %s", expectedBVT, pod.Name))
}

// ensureGroupIdentityEnabled patches slo-controller-config to enable GroupIdentity
// for LS and BE classes. Returns a rollback function that restores the previous config.
func ensureGroupIdentityEnabled(
	f *framework.Framework,
	c clientset.Interface,
	koordClient koordinatorclientset.Interface,
	koordNamespace, sloConfigName string,
) func() {
	configMap, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
	if err != nil {
		framework.Logf("slo-controller-config not found, skipping GroupIdentity config patch: %v", err)
		return nil
	}

	oldData := map[string]string{}
	if configMap.Data != nil {
		for k, v := range configMap.Data {
			oldData[k] = v
		}
	}

	// Patch the resource QoS config key.
	newConfigMap := configMap.DeepCopy()
	if newConfigMap.Data == nil {
		newConfigMap.Data = map[string]string{}
	}
	newConfigMap.Data[configuration.ResourceQOSConfigKey] = groupIdentityEnabledConfigData
	_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err, "failed to update slo-controller-config to enable GroupIdentity")
	framework.Logf("enabled GroupIdentity in slo-controller-config")

	// Return a rollback closure.
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
			framework.Logf("rolled back slo-controller-config to previous state")
		}
	}
}

// disableGroupIdentity patches the slo-controller-config to turn off CPU QoS.
func disableGroupIdentity(
	f *framework.Framework,
	c clientset.Interface,
	koordClient koordinatorclientset.Interface,
	koordNamespace, sloConfigName string,
) {
	const disabledData = `{
  "lsClass": {
    "cpuQOS": {
      "enable": false
    }
  },
  "beClass": {
    "cpuQOS": {
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
	framework.Logf("disabled GroupIdentity in slo-controller-config")
	// koordClient retained in signature for consistency with ensureGroupIdentityEnabled
	_ = koordClient
}
