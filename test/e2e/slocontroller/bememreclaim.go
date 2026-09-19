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
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
)

const (
	// beMemReclaimTargetMemoryLimit must match the standard memory limit of the target pod in
	// be-mem-reclaim-demo.yaml. kubelet turns it into the pod-level cgroup memory.max that the
	// reclaim watermark check reads.
	beMemReclaimTargetMemoryLimit = int64(1) << 30

	// beMemReclaimPSIThreshold mirrors memoryReclaimPSIThreshold of the cgreconcile plugin: reclaim
	// only fires when the kubepods-besteffort memory PSI some avg10 is at or above this value.
	beMemReclaimPSIThreshold = 5.0

	// beMemoryQOSConfigData enables BE memory QOS with the reclaim watermark and a non-zero
	// throttlingPercent. The throttling factor makes koordlet set memory.high on BE containers
	// from their batch-memory requests, which the pressurizer pod relies on to generate memory PSI.
	beMemoryQOSConfigData = `{
  "clusterStrategy": {
    "beClass": {
      "memoryQOS": {
        "enable": true,
        "wmarkRatio": 95,
        "throttlingPercent": 20
      }
    }
  }
}`

	// cgroupFileMissingMarker is echoed by the in-pod cgroup read helper when the requested cgroup
	// file does not exist (e.g. memory.pressure on kernels booted with PSI disabled).
	cgroupFileMissingMarker = "__KOORD_E2E_FILE_MISSING__"
)

// verbosityArgRE matches a klog verbosity argument like -v=4 or --v=4.
var verbosityArgRE = regexp.MustCompile(`^-{1,2}v=\d+$`)

// psiSomeAvg10RE extracts the "some" avg10 percentage from a cgroup memory.pressure file.
var psiSomeAvg10RE = regexp.MustCompile(`some avg10=([0-9.]+)`)

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

			// ---- Step 2: raise koordlet log verbosity ----
			// All reclaim log evidence is emitted at verbosity 5 or 6 while CI runs koordlet with
			// --v=4, so the log-based assertion below could never observe anything without this.
			raiseKoordletLogVerbosity(ctx, c, koordNamespace)

			// ---- Step 3: enable BE memory QOS via slo-controller-config ----
			// The nodeslo-controller recomputes every NodeSLO spec from this ConfigMap, so updating
			// the ConfigMap (instead of hand-writing a NodeSLO) is the only way to make the BE
			// memory QOS strategy stick on the node.
			enableBEMemoryQOS(ctx, c, koordClient, koordNamespace, nodeList.Items[0].Name)

			// ---- Step 4: create the pressurizer and the BE pod under test ----
			pressurizer, err := manifest.PodFromManifest("test/e2e/testing-manifests/slocontroller/be-mem-pressurizer.yaml")
			framework.ExpectNoError(err)
			pressurizer.Namespace = f.Namespace.Name
			pressurizer.Name = fmt.Sprintf("be-mem-pressurizer-%s", rand.String(6))
			framework.Logf("creating BE memory pressurizer pod %s/%s", pressurizer.Namespace, pressurizer.Name)
			_, err = c.CoreV1().Pods(f.Namespace.Name).Create(ctx, pressurizer, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			pod, err := manifest.PodFromManifest("test/e2e/testing-manifests/slocontroller/be-mem-reclaim-demo.yaml")
			framework.ExpectNoError(err)
			pod.Namespace = f.Namespace.Name
			// Append a random suffix to avoid name collisions across test runs.
			pod.Name = fmt.Sprintf("be-mem-reclaim-%s", rand.String(6))

			framework.Logf("creating BE pod %s/%s", pod.Namespace, pod.Name)
			_, err = c.CoreV1().Pods(f.Namespace.Name).Create(ctx, pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			// Wait for both pods to become Running and scheduled to a node.
			pressurizerNode := waitForPodRunningOnNode(ctx, c, f, pressurizer.Name)
			scheduledNode := waitForPodRunningOnNode(ctx, c, f, pod.Name)
			framework.Logf("pressurizer pod %s scheduled on node %s", pressurizer.Name, pressurizerNode)
			framework.Logf("BE pod %s scheduled on node %s", pod.Name, scheduledNode)

			// ---- Step 5: verify the environment can establish pressure; skip when it cannot ----
			// The pressurizer generates real memory PSI (required by the reclaim PSI gate) while the
			// BE pod fills itself beyond the reclaim watermark. If either condition cannot be
			// established in time, skip instead of failing the suite.
			verifyPressureOrSkip(ctx, c, f, f.Namespace.Name, pressurizer.Name, pod.Name)

			// ---- Step 6: verify reclaim via koordlet logs ----
			framework.Logf("waiting for koordlet to reclaim BE memory on node %s", scheduledNode)
			// Fetch a large tail: at verbosity 6 koordlet logs heavily and the reclaim evidence
			// recurs at the EAGAIN backoff cadence, so a small window can rotate past it.
			gomega.Eventually(func() bool {
				logs, logErr := getKoordletLogs(ctx, c, koordNamespace, scheduledNode, 20000)
				if logErr != nil {
					framework.Logf("failed to get koordlet logs: %v", logErr)
					return false
				}
				return strings.Contains(logs, "reclaimed BE pod")
			}, 3*time.Minute, 5*time.Second).Should(gomega.BeTrue(),
				"koordlet should have triggered memory reclaim for the BE pod exceeding the watermark")

			framework.Logf("koordlet reclaim activity confirmed on node %s", scheduledNode)
		})
	})
})

// waitForPodRunningOnNode waits until the pod becomes Running on a scheduled node and returns the
// node name.
func waitForPodRunningOnNode(ctx context.Context, c clientset.Interface, f *framework.Framework, name string) string {
	var scheduledNode string
	gomega.Eventually(func() bool {
		p, getErr := c.CoreV1().Pods(f.Namespace.Name).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return false
		}
		if p.Status.Phase == corev1.PodRunning && p.Spec.NodeName != "" {
			scheduledNode = p.Spec.NodeName
			return true
		}
		return false
	}, 2*time.Minute, 5*time.Second).Should(gomega.BeTrue(), fmt.Sprintf("pod %s should become Running", name))
	return scheduledNode
}

// raiseKoordletLogVerbosity patches the koordlet daemonset to log at verbosity 6 and waits for the
// rollout to finish. The original args are restored on test cleanup.
func raiseKoordletLogVerbosity(ctx context.Context, c clientset.Interface, koordNamespace string) {
	ds, err := c.AppsV1().DaemonSets(koordNamespace).Get(ctx, "koordlet", metav1.GetOptions{})
	framework.ExpectNoError(err)

	containerIdx := -1
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == "koordlet" {
			containerIdx = i
			break
		}
	}
	if containerIdx < 0 {
		framework.Failf("koordlet container not found in the koordlet daemonset")
	}
	origArgs := append([]string(nil), ds.Spec.Template.Spec.Containers[containerIdx].Args...)
	if hasArg(origArgs, "--v=6") {
		framework.Logf("koordlet already logs at verbosity 6")
		return
	}

	newArgs := setVerbosityArg(origArgs, "6")
	framework.Logf("raising koordlet log verbosity: %v -> %v", origArgs, newArgs)
	err = patchKoordletArgs(ctx, c, koordNamespace, newArgs)
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(func(ctx context.Context) {
		framework.Logf("restoring koordlet daemonset args to %v", origArgs)
		if restoreErr := patchKoordletArgs(ctx, c, koordNamespace, origArgs); restoreErr != nil {
			framework.Logf("failed to restore koordlet daemonset args: %v", restoreErr)
			return
		}
		waitKoordletReady(ctx, c, koordNamespace, "")
	})

	waitKoordletReady(ctx, c, koordNamespace, "--v=6")
	framework.Logf("koordlet restarted with --v=6")
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

// setVerbosityArg replaces the klog verbosity argument (e.g. --v=4) with the given level, or
// appends it when the daemonset does not set one.
func setVerbosityArg(args []string, level string) []string {
	out := append([]string(nil), args...)
	for i, arg := range out {
		if verbosityArgRE.MatchString(arg) {
			out[i] = "--v=" + level
			return out
		}
	}
	return append(out, "--v="+level)
}

func patchKoordletArgs(ctx context.Context, c clientset.Interface, koordNamespace string, args []string) error {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return err
	}
	patch := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":"koordlet","args":%s}]}}}}`, argsJSON)
	_, err = c.AppsV1().DaemonSets(koordNamespace).Patch(ctx, "koordlet", types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// waitKoordletReady waits until every koordlet pod is ready; when wantArg is non-empty the pod
// args must also contain it, i.e. the rollout to the new args has completed.
func waitKoordletReady(ctx context.Context, c clientset.Interface, koordNamespace, wantArg string) {
	gomega.Eventually(func() bool {
		pods, err := c.CoreV1().Pods(koordNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "koord-app=koordlet",
		})
		if err != nil || len(pods.Items) == 0 {
			return false
		}
		for i := range pods.Items {
			p := &pods.Items[i]
			if p.Status.Phase != corev1.PodRunning || !isPodReady(p) {
				return false
			}
			if wantArg != "" && !podArgsContain(p, "koordlet", wantArg) {
				return false
			}
		}
		return true
	}, 2*time.Minute, 3*time.Second).Should(gomega.BeTrue(), fmt.Sprintf("koordlet pods should become ready with args %q", wantArg))
}

func isPodReady(p *corev1.Pod) bool {
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podArgsContain(p *corev1.Pod, containerName, wantArg string) bool {
	for _, container := range p.Spec.Containers {
		if container.Name != containerName {
			continue
		}
		for _, arg := range container.Args {
			if arg == wantArg {
				return true
			}
		}
	}
	return false
}

// enableBEMemoryQOS updates the slo-controller-config ConfigMap so that the nodeslo-controller
// renders the BE memory QOS strategy (reclaim watermark + container memory.high throttling) into
// the NodeSLO of every node; the original value is restored on test cleanup.
func enableBEMemoryQOS(ctx context.Context, c clientset.Interface, koordClient koordinatorclientset.Interface,
	koordNamespace, nodeName string) {
	cm, err := c.CoreV1().ConfigMaps(koordNamespace).Get(ctx, framework.TestContext.SLOCtrlConfigMap, metav1.GetOptions{})
	framework.ExpectNoError(err)

	origData, existed := cm.Data[configuration.ResourceQOSConfigKey]
	ginkgo.DeferCleanup(func(ctx context.Context) {
		framework.Logf("restoring %s in %s/%s", configuration.ResourceQOSConfigKey, koordNamespace, framework.TestContext.SLOCtrlConfigMap)
		latest, getErr := c.CoreV1().ConfigMaps(koordNamespace).Get(ctx, framework.TestContext.SLOCtrlConfigMap, metav1.GetOptions{})
		if getErr != nil {
			framework.Logf("failed to get configmap for rollback: %v", getErr)
			return
		}
		updated := latest.DeepCopy()
		if existed {
			updated.Data[configuration.ResourceQOSConfigKey] = origData
		} else {
			delete(updated.Data, configuration.ResourceQOSConfigKey)
		}
		if _, updateErr := c.CoreV1().ConfigMaps(koordNamespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
			framework.Logf("failed to restore configmap: %v", updateErr)
		}
	})

	newCM := cm.DeepCopy()
	if newCM.Data == nil {
		newCM.Data = map[string]string{}
	}
	newCM.Data[configuration.ResourceQOSConfigKey] = beMemoryQOSConfigData
	_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(ctx, newCM, metav1.UpdateOptions{})
	framework.ExpectNoError(err)

	// Wait until the NodeSLO of the node carries the BE memory QOS strategy.
	gomega.Eventually(func() bool {
		nodeSLO, getErr := koordClient.SloV1alpha1().NodeSLOs().Get(ctx, nodeName, metav1.GetOptions{})
		if getErr != nil {
			framework.Logf("failed to get NodeSLO %s: %v", nodeName, getErr)
			return false
		}
		strategy := nodeSLO.Spec.ResourceQOSStrategy
		return strategy != nil && strategy.BEClass != nil && strategy.BEClass.MemoryQOS != nil &&
			strategy.BEClass.MemoryQOS.Enable != nil && *strategy.BEClass.MemoryQOS.Enable &&
			strategy.BEClass.MemoryQOS.WmarkRatio != nil && *strategy.BEClass.MemoryQOS.WmarkRatio == 95 &&
			strategy.BEClass.MemoryQOS.ThrottlingPercent != nil && *strategy.BEClass.MemoryQOS.ThrottlingPercent > 0
	}, 2*time.Minute, 5*time.Second).Should(gomega.BeTrue(),
		"NodeSLO should carry the BE memory QOS strategy from slo-controller-config")
	framework.Logf("NodeSLO %s carries the BE memory QOS strategy", nodeName)
}

// verifyPressureOrSkip waits until the environment generates real memory pressure, skipping the
// test when it cannot be established:
//  1. the kernel provides memory.reclaim, otherwise reclaim is unsupported regardless of pressure;
//  2. the pressurizer sustains memory PSI some avg10 above the reclaim gate threshold, or PSI is
//     not exposed at all (the production gate then passes by design);
//  3. the BE pod's memory usage crosses the reclaim watermark.
func verifyPressureOrSkip(ctx context.Context, c clientset.Interface, f *framework.Framework,
	namespace, pressurizerName, targetPodName string) {
	// The reclaim support gate reads memory.reclaim under a cgroup dir; make sure the kernel
	// provides the file at all before waiting on pressure.
	supported, err := podCgroupFileExists(ctx, c, f, namespace, pressurizerName, "pressurizer", "memory.reclaim")
	if err == nil && !supported {
		ginkgo.Skip("kernel does not support memory.reclaim; skipping the reclaim e2e")
	}

	deadline := time.Now().Add(90 * time.Second)
	psiSustained, usageAboveWatermark := false, false
	consecutivePSI := 0

	for time.Now().Before(deadline) {
		if !usageAboveWatermark {
			out, readErr := readPodCgroupFile(ctx, c, f, namespace, targetPodName, "pause", "memory.current")
			if readErr != nil {
				framework.Logf("failed to read memory.current of %s: %v", targetPodName, readErr)
			} else if v, parseErr := strconv.ParseUint(strings.TrimSpace(out), 10, 64); parseErr == nil {
				watermark := uint64(beMemReclaimTargetMemoryLimit) * 95 / 100
				if v > watermark {
					usageAboveWatermark = true
					framework.Logf("BE pod memory usage %d bytes is above the reclaim watermark %d", v, watermark)
				} else {
					framework.Logf("BE pod memory usage %d bytes, waiting to cross the reclaim watermark %d", v, watermark)
				}
			}
		}

		if !psiSustained {
			psi, enforced, psiErr := readMemoryPSISomeAvg10(ctx, c, f, namespace, pressurizerName, "pressurizer")
			switch {
			case psiErr != nil:
				framework.Logf("failed to read memory PSI of %s: %v", pressurizerName, psiErr)
				consecutivePSI = 0
			case !enforced:
				// memory.pressure is not exposed; the production PSI gate passes unconditionally.
				framework.Logf("memory PSI not exposed on this kernel, the PSI gate will not block")
				psiSustained = true
			default:
				framework.Logf("pressurizer memory PSI some avg10 = %.2f", psi)
				if psi >= beMemReclaimPSIThreshold {
					consecutivePSI++
					psiSustained = consecutivePSI >= 2
				} else {
					consecutivePSI = 0
				}
			}
		}

		if psiSustained && usageAboveWatermark {
			return
		}
		time.Sleep(5 * time.Second)
	}

	ginkgo.Skip("cannot generate memory pressure in this environment")
}

// execShellInPod runs a shell command inside the given pod container and returns stdout.
func execShellInPod(ctx context.Context, c clientset.Interface, f *framework.Framework,
	namespace, podName, containerName, script string) (string, error) {
	stdout, stderr, err := f.ExecWithOptions(framework.ExecOptions{
		Command:            []string{"/bin/sh", "-c", script},
		Namespace:          namespace,
		PodName:            podName,
		ContainerName:      containerName,
		CaptureStdout:      true,
		CaptureStderr:      true,
		PreserveWhitespace: false,
		Quiet:              true,
	})
	if err != nil {
		return "", fmt.Errorf("exec in %s/%s failed: %v, stderr: %s", namespace, podName, err, stderr)
	}
	return stdout, nil
}

// readPodCgroupFile reads a cgroup v2 file from inside the pod's container. The container's cgroup
// path is resolved from /proc/self/cgroup so it works with both host and private cgroup namespaces.
// When the file does not exist the returned output contains cgroupFileMissingMarker instead of an
// error, so callers can distinguish "missing" from other failures.
func readPodCgroupFile(ctx context.Context, c clientset.Interface, f *framework.Framework,
	namespace, podName, containerName, file string) (string, error) {
	script := fmt.Sprintf(`cg="$(sed -n 's/^0:://p' /proc/self/cgroup)"; cat "/sys/fs/cgroup${cg}/%s" 2>/dev/null || echo %s`, file, cgroupFileMissingMarker)
	return execShellInPod(ctx, c, f, namespace, podName, containerName, script)
}

// podCgroupFileExists reports whether a cgroup v2 file exists in the pod container's cgroup. It
// uses test -f because write-only cgroup files (e.g. memory.reclaim) cannot be read.
func podCgroupFileExists(ctx context.Context, c clientset.Interface, f *framework.Framework,
	namespace, podName, containerName, file string) (bool, error) {
	script := fmt.Sprintf(`cg="$(sed -n 's/^0:://p' /proc/self/cgroup)"; test -f "/sys/fs/cgroup${cg}/%s" && echo yes || echo no`, file)
	out, err := execShellInPod(ctx, c, f, namespace, podName, containerName, script)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "yes"), nil
}

// readMemoryPSISomeAvg10 reads the memory PSI of the pod container's cgroup. The second return
// value reports whether memory.pressure is exposed at all (it can be missing when the kernel booted
// with PSI disabled).
func readMemoryPSISomeAvg10(ctx context.Context, c clientset.Interface, f *framework.Framework,
	namespace, podName, containerName string) (float64, bool, error) {
	out, err := readPodCgroupFile(ctx, c, f, namespace, podName, containerName, "memory.pressure")
	if err != nil {
		return 0, false, err
	}
	if strings.Contains(out, cgroupFileMissingMarker) {
		return 0, false, nil
	}
	m := psiSomeAvg10RE.FindStringSubmatch(out)
	if m == nil {
		return 0, true, fmt.Errorf("cannot parse memory PSI output: %q", out)
	}
	psi, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, true, fmt.Errorf("cannot parse memory PSI avg10 %q: %v", m[1], err)
	}
	return psi, true, nil
}

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

// getKoordletLogs fetches the last tailLines lines of koordlet logs from the given node.
func getKoordletLogs(ctx context.Context, c clientset.Interface, koordNamespace, nodeName string, tailLines int64) (string, error) {
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
		TailLines: ptr.To(tailLines),
	}
	req := c.CoreV1().Pods(koordNamespace).GetLogs(pod.Name, logOpts)
	data, err := req.DoRaw(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get koordlet logs: %w", err)
	}
	return string(data), nil
}
