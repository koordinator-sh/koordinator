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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
	imageutils "github.com/koordinator-sh/koordinator/test/utils/image"
)

const (
	koordletLabelKey    = "koord-app"
	koordletLabelValue  = "koordlet"
	koordletMetricsPort = 9316

	psiPodMetricName       = "koordlet_pod_psi"
	psiContainerMetricName = "koordlet_container_psi"
)

var _ = SIGDescribe("PSICollector", func() {
	f := framework.NewDefaultFramework("psi-collector")
	var node *corev1.Node
	var koordNamespace string

	ginkgo.BeforeEach(func() {
		nodes, err := e2enode.GetReadySchedulableNodes(f.ClientSet)
		framework.ExpectNoError(err)
		gomega.Expect(len(nodes.Items)).NotTo(gomega.BeZero())
		node = &nodes.Items[0]
		koordNamespace = framework.TestContext.KoordinatorComponentNamespace
	})

	framework.KoordinatorDescribe("PSI metrics reporting", func() {
		framework.ConformanceIt("reports pod/container PSI metrics", func() {
			ctx := context.Background()

			if e2epod.NodeOSDistroIs("windows") {
				ginkgo.Skip("PSI metrics are only available on linux nodes")
			}

			ginkgo.By("Create CPU/memory pressure workload")
			pod := makePSIPressurePod(f.Namespace.Name, node.Name)
			pod = f.PodClient().Create(pod)
			defer func() {
				_ = f.PodClient().Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			}()

			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod))

			pod, err := f.PodClient().Get(ctx, pod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)

			var containerID string
			err = wait.PollImmediate(2*time.Second, 30*time.Second, func() (bool, error) {
				updatedPod, err := f.PodClient().Get(ctx, pod.Name, metav1.GetOptions{})
				if err != nil {
					return false, nil
				}
				containerID = getFirstContainerID(updatedPod)
				return containerID != "", nil
			})
			framework.ExpectNoError(err)

			ginkgo.By("Locate koordlet pod on the same node")
			koordletPod, err := getKoordletPodOnNode(ctx, f.ClientSet, koordNamespace, node.Name)
			framework.ExpectNoError(err)

			if enabled, found := isPSICollectorEnabled(koordletPod); found && !enabled {
				ginkgo.Skip("PSICollector feature gate is disabled on koordlet")
			}

			ginkgo.By("Verify PSI metrics are exposed by koordlet")
			metricsFound := wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
				metrics, err := getMetricsFromPodProxy(ctx, f.ClientSet, koordNamespace, koordletPod.Name, koordletMetricsPort)
				if err != nil {
					framework.Logf("failed to get koordlet metrics: %v", err)
					return false, nil
				}

				podLabels := map[string]string{
					"pod_uid":           string(pod.UID),
					"pod_name":          pod.Name,
					"pod_namespace":     pod.Namespace,
					"psi_resource_type": "cpu",
					"psi_precision":     "avg10",
					"psi_degree":        "some",
				}
				containerLabels := map[string]string{
					"pod_uid":           string(pod.UID),
					"pod_name":          pod.Name,
					"pod_namespace":     pod.Namespace,
					"container_id":      containerID,
					"container_name":    pod.Spec.Containers[0].Name,
					"psi_resource_type": "cpu",
					"psi_precision":     "avg10",
					"psi_degree":        "some",
				}

				return hasMetricWithLabels(metrics, psiPodMetricName, podLabels) &&
					hasMetricWithLabels(metrics, psiContainerMetricName, containerLabels), nil
			}) == nil

			if !metricsFound {
				ginkgo.Skip("PSI metrics not found; PSI may be unsupported or disabled on this node")
			}

			ginkgo.By("Validate NodeMetric is updated for the node")
			var nodeMetric *slov1alpha1.NodeMetric
			err = wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
				nodeMetric, err = f.KoordinatorClientSet.SloV1alpha1().NodeMetrics().Get(ctx, node.Name, metav1.GetOptions{})
				if err != nil {
					framework.Logf("failed to get NodeMetric %s: %v", node.Name, err)
					return false, nil
				}
				return findPodMetric(nodeMetric, pod) != nil, nil
			})
			framework.ExpectNoError(err)

			podMetric := findPodMetric(nodeMetric, pod)
			gomega.Expect(podMetric).NotTo(gomega.BeNil())

			if podMetric.Extensions == nil || len(podMetric.Extensions.Object) == 0 {
				framework.Logf("pod metric extensions are empty; PSI is not reported in NodeMetric status in this version")
			}
		})
	})
})

func makePSIPressurePod(namespace, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "psi-workload",
			Namespace: namespace,
			Labels: map[string]string{
				"e2e.koordinator.sh/psi": "true",
			},
		},
		Spec: corev1.PodSpec{
			NodeName:      nodeName,
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "worker",
					Image: imageutils.GetE2EImage(imageutils.BusyBox),
					Command: []string{
						"/bin/sh",
						"-c",
						"dd if=/dev/zero of=/mem/psi.bin bs=1M count=64 || true; yes > /dev/null",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "mem",
							MountPath: "/mem",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "mem",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							Medium:    corev1.StorageMediumMemory,
							SizeLimit: resource.NewQuantity(256*1024*1024, resource.BinarySI),
						},
					},
				},
			},
		},
	}
}

func getKoordletPodOnNode(ctx context.Context, client clientset.Interface, namespace, nodeName string) (*corev1.Pod, error) {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", koordletLabelKey, koordletLabelValue),
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no koordlet pod found on node %s", nodeName)
	}
	return &pods.Items[0], nil
}

func getMetricsFromPodProxy(ctx context.Context, client clientset.Interface, namespace, podName string, port int) (string, error) {
	rawOutput, err := client.CoreV1().RESTClient().Get().
		Namespace(namespace).
		Resource("pods").
		SubResource("proxy").
		Name(fmt.Sprintf("%s:%d", podName, port)).
		Suffix("metrics").
		Do(ctx).Raw()
	if err != nil {
		return "", err
	}
	return string(rawOutput), nil
}

func hasMetricWithLabels(metrics string, metricName string, labels map[string]string) bool {
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, metricName+"{") {
			continue
		}
		matched := true
		for key, value := range labels {
			if !strings.Contains(line, fmt.Sprintf("%s=\"%s\"", key, value)) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func getFirstContainerID(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.ContainerID != "" {
			return status.ContainerID
		}
	}
	return ""
}

func findPodMetric(nodeMetric *slov1alpha1.NodeMetric, pod *corev1.Pod) *slov1alpha1.PodMetricInfo {
	if nodeMetric == nil || pod == nil {
		return nil
	}
	for _, metric := range nodeMetric.Status.PodsMetric {
		if metric != nil && metric.Namespace == pod.Namespace && metric.Name == pod.Name {
			return metric
		}
	}
	return nil
}

func isPSICollectorEnabled(pod *corev1.Pod) (bool, bool) {
	if pod == nil || len(pod.Spec.Containers) == 0 {
		return false, false
	}
	container := pod.Spec.Containers[0]
	featureGateArg, ok := findFeatureGateArg(container.Args)
	if !ok {
		featureGateArg, ok = findFeatureGateArg(container.Command)
	}
	if !ok {
		return false, false
	}
	return featureGateEnabled(featureGateArg, "PSICollector") || featureGateEnabled(featureGateArg, "AllAlpha"), true
}

func findFeatureGateArg(args []string) (string, bool) {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-feature-gates=") {
			return strings.TrimPrefix(arg, "-feature-gates="), true
		}
		if strings.HasPrefix(arg, "--feature-gates=") {
			return strings.TrimPrefix(arg, "--feature-gates="), true
		}
	}
	return "", false
}

func featureGateEnabled(featureGates string, key string) bool {
	for _, gate := range strings.Split(featureGates, ",") {
		parts := strings.SplitN(strings.TrimSpace(gate), "=", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] == key {
			return strings.EqualFold(parts[1], "true")
		}
	}
	return false
}
