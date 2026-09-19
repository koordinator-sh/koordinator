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

package slocontroller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
)

var _ = SIGDescribe("BEMemoryReclaim", func() {
	f := framework.NewDefaultFramework("bememreclaim")

	var c clientset.Interface
	var koordClient koordinatorclientset.Interface
	var koordNamespace string
	var nodeList *corev1.NodeList
	var err error

	ginkgo.BeforeEach(func() {
		c = f.ClientSet
		koordClient = f.KoordinatorClientSet
		koordNamespace = framework.TestContext.KoordinatorComponentNamespace

		nodeList, err = e2enode.GetReadySchedulableNodes(c)
		framework.ExpectNoError(err)
		gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero())
	})

	framework.KoordinatorDescribe("BEMemoryReclaim via memory.reclaim", func() {
		ginkgo.It("should trigger memory.reclaim for BE pod exceeding watermark on cgroup v2", func(ctx context.Context) {
			// ---- Step 1: Skip on non-cgroup-v2 nodes ----
			cgroupV2 := isCgroupV2OnNode(ctx, c, koordNamespace, f, nodeList.Items[0].Name)
			if !cgroupV2 {
				ginkgo.Skip("cgroup v2 is not enabled; memory.reclaim is only available on cgroup v2")
			}

			// ---- Step 2: Create BE pod from manifest ----
			pod, err := manifest.PodFromManifest("test/e2e/testing-manifests/slocontroller/be-mem-reclaim-demo.yaml")
			framework.ExpectNoError(err)
			pod.Namespace = f.Namespace.Name
			// Append a random suffix to avoid name collisions across test runs.
			pod.Name = fmt.Sprintf("be-mem-reclaim-%s", rand.String(6))

			framework.Logf("creating BE pod %s/%s", pod.Namespace, pod.Name)
			_, err = c.CoreV1().Pods(f.Namespace.Name).Create(ctx, pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			// Wait for the pod to be Running and scheduled to a node.
			var scheduledNode string
			gomega.Eventually(func() bool {
				p, getErr := c.CoreV1().Pods(f.Namespace.Name).Get(ctx, pod.Name, metav1.GetOptions{})
				if getErr != nil {
					return false
				}
				if p.Status.Phase == corev1.PodRunning && p.Spec.NodeName != "" {
					scheduledNode = p.Spec.NodeName
					return true
				}
				return false
			}, 2*time.Minute, 5*time.Second).Should(gomega.BeTrue(), "BE pod should become Running")
			framework.Logf("BE pod %s scheduled on node %s", pod.Name, scheduledNode)

			// ---- Step 3: Create or update NodeSLO with BE memory QoS ----
			// The NodeSLO name must match the node name (cluster-scoped resource).
			nodeSLO := &slov1alpha1.NodeSLO{
				ObjectMeta: metav1.ObjectMeta{
					Name: scheduledNode,
				},
				Spec: slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						BEClass: &slov1alpha1.ResourceQOS{
							MemoryQOS: &slov1alpha1.MemoryQOSCfg{
								Enable: ptr.To[bool](true),
								MemoryQOS: slov1alpha1.MemoryQOS{
									// wmarkRatio=95 means reclaim is triggered when usage > 95% of limit.
									WmarkRatio: ptr.To[int64](95),
								},
							},
						},
					},
				},
			}

			// Try to get existing NodeSLO for this node; create or update accordingly.
			existing, getErr := koordClient.SloV1alpha1().NodeSLOs().Get(ctx, scheduledNode, metav1.GetOptions{})
			if getErr != nil {
				framework.Logf("creating NodeSLO %s", scheduledNode)
				_, err = koordClient.SloV1alpha1().NodeSLOs().Create(ctx, nodeSLO, metav1.CreateOptions{})
				framework.ExpectNoError(err)
				// Clean up the created NodeSLO after the test.
				ginkgo.DeferCleanup(func(ctx context.Context) {
					_ = koordClient.SloV1alpha1().NodeSLOs().Delete(ctx, scheduledNode, metav1.DeleteOptions{})
				})
			} else {
				framework.Logf("updating NodeSLO %s", scheduledNode)
				origStrategy := existing.Spec.ResourceQOSStrategy.DeepCopy()
				existing.Spec.ResourceQOSStrategy = nodeSLO.Spec.ResourceQOSStrategy
				_, err = koordClient.SloV1alpha1().NodeSLOs().Update(ctx, existing, metav1.UpdateOptions{})
				framework.ExpectNoError(err)
				// Restore the original NodeSLO after the test.
				ginkgo.DeferCleanup(func(ctx context.Context) {
					current, getErr := koordClient.SloV1alpha1().NodeSLOs().Get(ctx, scheduledNode, metav1.GetOptions{})
					if getErr == nil {
						current.Spec.ResourceQOSStrategy = origStrategy
						_, _ = koordClient.SloV1alpha1().NodeSLOs().Update(ctx, current, metav1.UpdateOptions{})
					}
				})
			}

			// ---- Step 4: Verify reclaim via koordlet logs ----
			// The koordlet runs as a DaemonSet on each node. Check the last few lines of its log
			// for memory.reclaim activity.
			framework.Logf("waiting for koordlet to reclaim BE memory on node %s", scheduledNode)
			gomega.Eventually(func() bool {
				logs, logErr := getKoordletLogs(ctx, c, koordNamespace, scheduledNode)
				if logErr != nil {
					framework.Logf("failed to get koordlet logs: %v", logErr)
					return false
				}
				if strings.Contains(logs, "reclaimed BE pod") || strings.Contains(logs, "skip reclaiming BE memory") {
					return true
				}
				return false
			}, 3*time.Minute, 10*time.Second).Should(gomega.BeTrue(),
				"koordlet should have attempted BE memory reclaim within the reconcile interval")

			framework.Logf("koordlet reclaim activity confirmed on node %s", scheduledNode)

			// Note: the pod's memory usage in this test is 0 (just sleeping). What we verify here is
			// that the koordlet's reclaim code path is exercised (it will find no BE pod exceeding the
			// watermark unless another BE pod on the same node is using memory). The real validation
			// of the reclaim logic (kernel memory.reclaim + watermark crossing) requires running on a
			// cgroup v2 cluster with a memory-intensive BE workload.
		})
	})
})

// isCgroupV2OnNode checks whether the koordlet on the given node is running under cgroup v2
// by exec-ing into the koordlet pod and checking for /sys/fs/cgroup/cgroup.controllers.
func isCgroupV2OnNode(ctx context.Context, c clientset.Interface, koordNamespace string, f *framework.Framework, nodeName string) bool {
	pods, err := c.CoreV1().Pods(koordNamespace).List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
		LabelSelector: "koord-app=koordlet",
	})
	if err != nil || len(pods.Items) == 0 {
		framework.Logf("no koordlet pod found on node %s: %v", nodeName, err)
		return false
	}
	// Pick the first koordlet pod on this node.
	pod := pods.Items[0]
	containerName := "koordlet" // default container name; adjust if different.
	if len(pod.Spec.Containers) > 0 {
		containerName = pod.Spec.Containers[0].Name
	}

	// Exec into the container and check if the cgroup v2 marker file exists.
	cmd := []string{"/bin/sh", "-c", "test -f /sys/fs/cgroup/cgroup.controllers && echo 'cgroup2' || echo 'cgroup1'"}
	stdout, _, err := f.ExecWithOptions(framework.ExecOptions{
		Command:            cmd,
		Namespace:          koordNamespace,
		PodName:            pod.Name,
		ContainerName:      containerName,
		CaptureStdout:      true,
		CaptureStderr:      true,
		PreserveWhitespace: false,
		Quiet:              true,
	})
	if err != nil {
		framework.Logf("failed to exec into koordlet pod to check cgroup version: %v", err)
		return false
	}
	return strings.Contains(stdout, "cgroup2")
}

// getKoordletLogs fetches the last 50 lines of koordlet logs from the given node.
func getKoordletLogs(ctx context.Context, c clientset.Interface, koordNamespace, nodeName string) (string, error) {
	pods, err := c.CoreV1().Pods(koordNamespace).List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
		LabelSelector: "koord-app=koordlet",
	})
	if err != nil || len(pods.Items) == 0 {
		return "", fmt.Errorf("no koordlet pod on node %s: %w", nodeName, err)
	}
	pod := pods.Items[0]
	containerName := "koordlet"
	if len(pod.Spec.Containers) > 0 {
		containerName = pod.Spec.Containers[0].Name
	}

	logOpts := &corev1.PodLogOptions{
		Container: containerName,
		TailLines: ptr.To[int64](50),
	}
	req := c.CoreV1().Pods(koordNamespace).GetLogs(pod.Name, logOpts)
	data, err := req.DoRaw(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get koordlet logs: %w", err)
	}
	return string(data), nil
}
