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

package scheduling

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	clientgov1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("LimitAware", func() {
	updateNode := func(nodeClient clientgov1.NodeInterface, nodeName string, updateFn func(node *corev1.Node)) {
		framework.ExpectNoError(wait.Poll(time.Millisecond*500, time.Second*30, func() (bool, error) {
			node, err := nodeClient.Get(context.TODO(), nodeName, metav1.GetOptions{})
			if err != nil {
				return false, fmt.Errorf("failed to get node %q: %v", nodeName, err)
			}
			updateFn(node)
			_, err = nodeClient.UpdateStatus(context.TODO(), node, metav1.UpdateOptions{})
			if err == nil {
				klog.Infof("Successfully update node %s", nodeName)
				return true, nil
			}
			if apierrors.IsConflict(err) {
				klog.Infof("Conflicting update to node %q, re-get and re-update: %v", nodeName, err)
				return false, nil
			}
			return false, fmt.Errorf("failed to update node %q: %v", nodeName, err)
		}))
	}
	f := framework.NewDefaultFramework("limit-aware")
	framework.KoordinatorDescribe("Normal LimitAware Case", func() {
		framework.ConformanceIt("limit aware filter", func() {
			nodeName := runPodAndGetNodeName(f, pausePodConfig{
				Name:      "without-label",
				Resources: &corev1.ResourceRequirements{},
				Tolerations: []corev1.Toleration{{
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoSchedule,
				}},
				SchedulerName: "koord-scheduler",
			})
			nodeClient := f.ClientSet.CoreV1().Nodes()
			node, err := nodeClient.Get(context.TODO(), nodeName, metav1.GetOptions{})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("Loading Pod from manifest")
			pod, err := manifest.PodFromManifest("scheduling/simple-pod.yaml")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			pod.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU] = node.Status.Allocatable[corev1.ResourceCPU]
			pod.Spec.Containers[0].Resources.Limits = quotav1.Add(pod.Spec.Containers[0].Resources.Limits, corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")})
			e2epod.SetNodeAffinity(&pod.Spec, nodeName)

			ginkgo.By("Create Pod")
			pod.Namespace = f.Namespace.Name
			pod.Name = string(uuid.NewUUID())
			pod.Spec.Tolerations = append(pod.Spec.Tolerations, corev1.Toleration{
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			})
			pod = f.PodClient().Create(pod)

			ginkgo.By("Wait for Pod schedule failed")
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
			}))

			ginkgo.By("Annotate Node With LimitAllocatable Ratio")
			updateNode(nodeClient, nodeName, func(node *corev1.Node) {
				limitAllocatable := extension.LimitToAllocatableRatio{
					corev1.ResourceCPU: intstr.FromInt(300),
				}
				ratioJSON, err := json.Marshal(limitAllocatable)
				node.Annotations[extension.AnnotationLimitToAllocatable] = string(ratioJSON)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			})
			defer func() {
				ginkgo.By("clear node annotation")
				updateNode(nodeClient, nodeName, func(node *corev1.Node) {
					delete(node.Annotations, extension.AnnotationLimitToAllocatable)
				})
			}()

			ginkgo.By("Wait for Pod Scheduled")
			gomega.Eventually(func() bool {
				p, err := f.PodClient().Get(context.TODO(), pod.Name, metav1.GetOptions{})
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				_, podCondition := k8spodutil.GetPodCondition(&p.Status, corev1.PodScheduled)
				return podCondition != nil && podCondition.Status == corev1.ConditionTrue && p.Status.Phase == corev1.PodRunning
			}, 60*time.Second, 3*time.Second).Should(gomega.Equal(true))

		})
	})
})
