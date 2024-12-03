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
	"strings"
	"time"

	nrtclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("NodeNUMAResource", func() {
	f := framework.NewDefaultFramework("nodenumaresource")
	var koordSchedulerName string

	ginkgo.BeforeEach(func() {
		koordSchedulerName = framework.TestContext.KoordSchedulerName
	})

	framework.KoordinatorDescribe("NodeNUMAResource CPUBindPolicy", func() {
		framework.ConformanceIt("bind with SpreadByPCPUs", func() {
			ginkgo.By("Loading Pod from manifest")
			pod, err := manifest.PodFromManifest("scheduling/simple-lsr-pod.yaml")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			pod.Namespace = f.Namespace.Name

			ginkgo.By("Create Pod")
			pod = f.PodClient().Create(pod)

			ginkgo.By("Wait for Pod Scheduled")
			gomega.Eventually(func() bool {
				p, err := f.PodClient().Get(context.TODO(), pod.Name, metav1.GetOptions{})
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				_, podCondition := k8spodutil.GetPodCondition(&p.Status, corev1.PodScheduled)
				return podCondition != nil && podCondition.Status == corev1.ConditionTrue
			}, 60*time.Second, 3*time.Second).Should(gomega.Equal(true))

			ginkgo.By("Check Pod ResourceStatus")
			pod, err = f.PodClient().Get(context.TODO(), pod.Name, metav1.GetOptions{})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(resourceStatus.CPUSet).NotTo(gomega.BeEmpty())
		})
	})

	ginkgo.Context("Reservation reserves CPUSet", func() {
		ginkgo.BeforeEach(func() {

		})

		ginkgo.AfterEach(func() {
			ls := metav1.SetAsLabelSelector(map[string]string{
				"e2e-test-reservation": "true",
			})
			reservationList, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().List(context.TODO(), metav1.ListOptions{
				LabelSelector: metav1.FormatLabelSelector(ls),
			})
			framework.ExpectNoError(err)
			for _, v := range reservationList.Items {
				err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Delete(context.TODO(), v.Name, metav1.DeleteOptions{})
				framework.ExpectNoError(err)
			}
		})

		framework.ConformanceIt("basic allocate cpuset from reservation", func() {
			ginkgo.By("Create reservation and reserve cpuset")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			resourceSpec := &extension.ResourceSpec{
				PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
			}
			data, err := json.Marshal(resourceSpec)
			framework.ExpectNoError(err)
			if reservation.Spec.Template.Annotations == nil {
				reservation.Spec.Template.Annotations = map[string]string{}
			}
			reservation.Spec.Template.Annotations[extension.AnnotationResourceSpec] = string(data)
			if reservation.Spec.Template.Labels == nil {
				reservation.Spec.Template.Labels = map[string]string{}
			}
			reservation.Spec.Template.Labels[extension.LabelPodQoS] = string(extension.QoSLSR)
			reservation.Spec.Template.Spec.PriorityClassName = string(extension.PriorityProd)
			reservation.Spec.Template.Spec.Priority = pointer.Int32(extension.PriorityProdValueMax)
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			}
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-cpuset-reservation": "true",
						},
					},
				},
			}
			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod to obtain cpuset from reservation")
			pod := createPausePod(f, pausePodConfig{
				Name: "test-cpu-reservation-pod",
				Labels: map[string]string{
					"test-cpuset-reservation": "true",
					extension.LabelPodQoS:     string(extension.QoSLSR),
				},
				Annotations: map[string]string{
					extension.AnnotationResourceSpec: string(data),
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("100Mi"),
					},
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("100Mi"),
					},
				},
				PriorityClassName: string(extension.PriorityProd),
				SchedulerName:     koordSchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod), "unable schedule the lowest priority pod")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, pod.Namespace, pod.Name, reservation.Name)

			pod, err = f.PodClient().Get(context.TODO(), pod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get pod")

			podResourceStatus, err := extension.GetResourceStatus(pod.Annotations)
			framework.ExpectNoError(err, "unable to GetResourceStatus from pod")

			reservationResourceStatus, err := extension.GetResourceStatus(reservation.Annotations)
			framework.ExpectNoError(err, "unable to GetResourceStatus from reservation")
			framework.ExpectEqual(podResourceStatus, reservationResourceStatus)
		})

		framework.ConformanceIt("basic allocs cpuset from reservation and node", func() {
			ginkgo.By("Create reservation and reserve cpuset")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			resourceSpec := &extension.ResourceSpec{
				PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
			}
			data, err := json.Marshal(resourceSpec)
			framework.ExpectNoError(err)
			if reservation.Spec.Template.Annotations == nil {
				reservation.Spec.Template.Annotations = map[string]string{}
			}
			reservation.Spec.Template.Annotations[extension.AnnotationResourceSpec] = string(data)
			if reservation.Spec.Template.Labels == nil {
				reservation.Spec.Template.Labels = map[string]string{}
			}
			reservation.Spec.Template.Labels[extension.LabelPodQoS] = string(extension.QoSLSR)
			reservation.Spec.Template.Spec.PriorityClassName = string(extension.PriorityProd)
			reservation.Spec.Template.Spec.Priority = pointer.Int32(extension.PriorityProdValueMax)
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			}
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-cpuset-reservation": "true",
						},
					},
				},
			}
			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod to obtain cpuset from reservation")
			pod := createPausePod(f, pausePodConfig{
				Name: "test-cpu-reservation-pod",
				Labels: map[string]string{
					"test-cpuset-reservation": "true",
					extension.LabelPodQoS:     string(extension.QoSLSR),
				},
				Annotations: map[string]string{
					extension.AnnotationResourceSpec: string(data),
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("100Mi"),
					},
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("100Mi"),
					},
				},
				NodeName:          reservation.Status.NodeName,
				PriorityClassName: string(extension.PriorityProd),
				SchedulerName:     koordSchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod), "unable schedule the lowest priority pod")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, pod.Namespace, pod.Name, reservation.Name)

			pod, err = f.PodClient().Get(context.TODO(), pod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get pod")

			podResourceStatus, err := extension.GetResourceStatus(pod.Annotations)
			framework.ExpectNoError(err, "unable to GetResourceStatus from pod")
			cpus, err := cpuset.Parse(podResourceStatus.CPUSet)
			framework.ExpectNoError(err)

			reservationResourceStatus, err := extension.GetResourceStatus(reservation.Annotations)
			framework.ExpectNoError(err, "unable to GetResourceStatus from reservation")
			reservedCPUs, err := cpuset.Parse(reservationResourceStatus.CPUSet)
			framework.ExpectNoError(err)

			gomega.Expect(reservedCPUs.IsSubsetOf(cpus)).Should(gomega.BeTrue())
		})

		framework.ConformanceIt("reusable reservation reserves cpuset", func() {
			ginkgo.By("Create reservation and reserve cpuset")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			resourceSpec := &extension.ResourceSpec{
				PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
			}
			data, err := json.Marshal(resourceSpec)
			framework.ExpectNoError(err)
			if reservation.Spec.Template.Annotations == nil {
				reservation.Spec.Template.Annotations = map[string]string{}
			}
			reservation.Spec.Template.Annotations[extension.AnnotationResourceSpec] = string(data)
			if reservation.Spec.Template.Labels == nil {
				reservation.Spec.Template.Labels = map[string]string{}
			}
			reservation.Spec.Template.Labels[extension.LabelPodQoS] = string(extension.QoSLSR)
			reservation.Spec.Template.Spec.PriorityClassName = string(extension.PriorityProd)
			reservation.Spec.Template.Spec.Priority = pointer.Int32(extension.PriorityProdValueMax)
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			}
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-cpuset-reservation": "true",
						},
					},
				},
			}
			reservation.Spec.AllocateOnce = pointer.Bool(false)
			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			reservationResourceStatus, err := extension.GetResourceStatus(reservation.Annotations)
			framework.ExpectNoError(err, "unable to GetResourceStatus from reservation")
			reservedCPUs, err := cpuset.Parse(reservationResourceStatus.CPUSet)
			framework.ExpectNoError(err)

			ginkgo.By("Create pod to obtain cpuset from reservation")

			ginkgo.By("Create 2 pods")
			replicas := 2
			rsConfig := pauseRSConfig{
				Replicas: int32(replicas),
				PodConfig: pausePodConfig{
					Name:      "test-cpuset-reusable-reservation",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						"test-cpuset-reservation": "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				},
			}
			runPauseRS(f, rsConfig)
			podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err, "unable to List Pods")
			var prePodCPUs cpuset.CPUSet
			for i := range podList.Items {
				pod := &podList.Items[i]
				framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod), "unable schedule the lowest priority pod")
				expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, pod.Namespace, pod.Name, reservation.Name)

				podResourceStatus, err := extension.GetResourceStatus(pod.Annotations)
				framework.ExpectNoError(err, "unable to GetResourceStatus from pod")
				cpus, err := cpuset.Parse(podResourceStatus.CPUSet)
				framework.ExpectNoError(err)
				gomega.Expect(cpus.IsSubsetOf(reservedCPUs)).Should(gomega.BeTrue())
				if !prePodCPUs.IsEmpty() {
					gomega.Expect(cpus.Intersection(prePodCPUs).IsEmpty()).Should(gomega.BeTrue())
				}
				prePodCPUs = cpus
			}
		})
	})

	ginkgo.Context("NUMA Topology Policy", func() {
		var targetNodeName string
		var nrtClient nrtclientset.Interface

		ginkgo.BeforeEach(func() {
			targetNodeName = ""
			config := f.ClientConfig()
			var err error
			nrtClient, err = nrtclientset.NewForConfig(config)
			framework.ExpectNoError(err, "unable to create NRT ClientSet")
		})

		ginkgo.AfterEach(func() {
			if targetNodeName != "" {
				framework.RemoveLabelOffNode(f.ClientSet, targetNodeName, extension.LabelNUMATopologyPolicy)
			}
		})

		framework.ConformanceIt("NUMA topology is set on pod, the pod is scheduled on a node without any policy", func() {
			nrt := getSuitableNodeResourceTopology(nrtClient, 2)
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nrt.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node")
			targetNodeName = node.Name
			ginkgo.By("Create two pods allocate 56% resources of Node, and every Pod allocates 28% resources per NUMA Node")
			cpuQuantity := node.Status.Allocatable[corev1.ResourceCPU]
			memoryQuantity := node.Status.Allocatable[corev1.ResourceMemory]
			percent := intstr.FromString("28%")
			cpu, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests := corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			rsConfig := pauseRSConfig{
				Replicas: int32(2),
				PodConfig: pausePodConfig{
					Name:      "request-resource-with-single-numa-pod",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						"test-app": "true",
					},
					Annotations: map[string]string{
						extension.AnnotationNUMATopologySpec: `{"numaTopologyPolicy":"SingleNUMANode"}`,
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				},
			}
			runPauseRS(f, rsConfig)

			ginkgo.By("Check the two pods allocated in two NUMA Nodes")
			podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err)
			nodes := sets.NewInt()
			for i := range podList.Items {
				pod := &podList.Items[i]
				ginkgo.By(fmt.Sprintf("pod %q, resourceStatus: %s", pod.Name, pod.Annotations[extension.AnnotationResourceStatus]))
				resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
				framework.ExpectNoError(err, "invalid resourceStatus")
				gomega.Expect(len(resourceStatus.NUMANodeResources)).Should(gomega.Equal(1))
				r := equality.Semantic.DeepEqual(resourceStatus.NUMANodeResources[0].Resources, requests)
				gomega.Expect(r).Should(gomega.Equal(true))
				gomega.Expect(false).Should(gomega.Equal(nodes.Has(int(resourceStatus.NUMANodeResources[0].Node))))
				nodes.Insert(int(resourceStatus.NUMANodeResources[0].Node))
			}
			gomega.Expect(nodes.Len()).Should(gomega.Equal(2))

			ginkgo.By("Create the third Pod allocates 30% resources of Node, expect it failed to schedule")
			percent = intstr.FromString("30%")
			cpu, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests = corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			pod := createPausePod(f, pausePodConfig{
				Name:      "must-be-failed-pod",
				Namespace: f.Namespace.Name,
				Resources: &corev1.ResourceRequirements{
					Limits:   requests,
					Requests: requests,
				},
				SchedulerName: koordSchedulerName,
				NodeName:      node.Name,
			})
			ginkgo.By("Wait for Pod schedule failed")
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				if scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse &&
					strings.Contains(scheduledCondition.Message, "NUMA Topology affinity") {
					return true, nil
				}
				return false, nil
			}))
		})

		// SingleNUMANode with 2 NUMA Nodes
		framework.ConformanceIt("SingleNUMANode with 2 NUMA Nodes", func() {
			nrt := getSuitableNodeResourceTopology(nrtClient, 2)
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nrt.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node")

			ginkgo.By(fmt.Sprintf("Label node %s with %s=%s", node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode)))
			framework.AddOrUpdateLabelOnNode(f.ClientSet, node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode))
			targetNodeName = node.Name

			ginkgo.By("Create two pods allocate 56% resources of Node, and every Pod allocates 28% resources per NUMA Node")
			cpuQuantity := node.Status.Allocatable[corev1.ResourceCPU]
			memoryQuantity := node.Status.Allocatable[corev1.ResourceMemory]
			percent := intstr.FromString("28%")
			cpu, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests := corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			rsConfig := pauseRSConfig{
				Replicas: int32(2),
				PodConfig: pausePodConfig{
					Name:      "request-resource-with-single-numa-pod",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						"test-app": "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				},
			}
			runPauseRS(f, rsConfig)

			ginkgo.By("Check the two pods allocated in two NUMA Nodes")
			podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err)
			nodes := sets.NewInt()
			for i := range podList.Items {
				pod := &podList.Items[i]
				ginkgo.By(fmt.Sprintf("pod %q, resourceStatus: %s", pod.Name, pod.Annotations[extension.AnnotationResourceStatus]))
				resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
				framework.ExpectNoError(err, "invalid resourceStatus")
				gomega.Expect(len(resourceStatus.NUMANodeResources)).Should(gomega.Equal(1))
				r := equality.Semantic.DeepEqual(resourceStatus.NUMANodeResources[0].Resources, requests)
				gomega.Expect(r).Should(gomega.Equal(true))
				gomega.Expect(false).Should(gomega.Equal(nodes.Has(int(resourceStatus.NUMANodeResources[0].Node))))
				nodes.Insert(int(resourceStatus.NUMANodeResources[0].Node))
			}
			gomega.Expect(nodes.Len()).Should(gomega.Equal(2))

			ginkgo.By("Create the third Pod allocates 30% resources of Node, expect it failed to schedule")
			percent = intstr.FromString("30%")
			cpu, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests = corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			pod := createPausePod(f, pausePodConfig{
				Name:      "must-be-failed-pod",
				Namespace: f.Namespace.Name,
				Resources: &corev1.ResourceRequirements{
					Limits:   requests,
					Requests: requests,
				},
				SchedulerName: koordSchedulerName,
				NodeName:      node.Name,
			})
			ginkgo.By("Wait for Pod schedule failed")
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				if scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse &&
					strings.Contains(scheduledCondition.Message, "NUMA Topology affinity") {
					return true, nil
				}
				return false, nil
			}))
		})

		framework.ConformanceIt("LSR pods apply most resources on Node which enables SingleNUMANode policy, failed to scheduling LS Pod", func() {
			nrt := getSuitableNodeResourceTopology(nrtClient, 2)
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nrt.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node")

			ginkgo.By(fmt.Sprintf("Label node %s with %s=%s", node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode)))
			framework.AddOrUpdateLabelOnNode(f.ClientSet, node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode))
			targetNodeName = node.Name

			ginkgo.By("Create two pods allocate 56% resources of Node, and every Pod allocates 28% resources per NUMA Node")
			cpuQuantity := node.Status.Allocatable[corev1.ResourceCPU]
			memoryQuantity := node.Status.Allocatable[corev1.ResourceMemory]
			percent := intstr.FromString("28%")
			cpu, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			cpu -= cpu % 1000
			memory, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests := corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			rsConfig := pauseRSConfig{
				Replicas: int32(2),
				PodConfig: pausePodConfig{
					Name:      "request-resource-with-single-numa-pod",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						"test-app":            "true",
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					PriorityClassName: string(extension.PriorityProd),
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				},
			}
			runPauseRS(f, rsConfig)

			ginkgo.By("Check the two pods allocated in two NUMA Nodes")
			podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err)
			nodes := sets.NewInt()
			for i := range podList.Items {
				pod := &podList.Items[i]
				ginkgo.By(fmt.Sprintf("pod %q, resourceStatus: %s", pod.Name, pod.Annotations[extension.AnnotationResourceStatus]))
				resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
				framework.ExpectNoError(err, "invalid resourceStatus")
				gomega.Expect(len(resourceStatus.NUMANodeResources)).Should(gomega.Equal(1))
				r := equality.Semantic.DeepEqual(resourceStatus.NUMANodeResources[0].Resources, requests)
				gomega.Expect(r).Should(gomega.Equal(true))
				gomega.Expect(false).Should(gomega.Equal(nodes.Has(int(resourceStatus.NUMANodeResources[0].Node))))
				nodes.Insert(int(resourceStatus.NUMANodeResources[0].Node))
				gomega.Expect(resourceStatus.CPUSet).Should(gomega.Not(gomega.BeEmpty()))
			}
			gomega.Expect(nodes.Len()).Should(gomega.Equal(2))

			ginkgo.By("Create the third LS Pod allocates 30% resources of Node, expect it failed to schedule")
			percent = intstr.FromString("30%")
			cpu, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests = corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			pod := createPausePod(f, pausePodConfig{
				Name:      "must-be-failed-pod",
				Namespace: f.Namespace.Name,
				Labels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSLS),
				},
				PriorityClassName: string(extension.PriorityProd),
				Resources: &corev1.ResourceRequirements{
					Limits:   requests,
					Requests: requests,
				},
				SchedulerName: koordSchedulerName,
				NodeName:      node.Name,
			})
			ginkgo.By("Wait for Pod schedule failed")
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				if scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse &&
					strings.Contains(scheduledCondition.Message, "NUMA Topology affinity") {
					return true, nil
				}
				return false, nil
			}))
		})

		framework.ConformanceIt("LS pods apply most resources on Node which enables SingleNUMANode policy, failed to scheduling LSR Pod", func() {
			nrt := getSuitableNodeResourceTopology(nrtClient, 2)
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nrt.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node")

			ginkgo.By(fmt.Sprintf("Label node %s with %s=%s", node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode)))
			framework.AddOrUpdateLabelOnNode(f.ClientSet, node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode))
			targetNodeName = node.Name

			ginkgo.By("Create two pods allocate 56% resources of Node, and every Pod allocates 28% resources per NUMA Node")
			cpuQuantity := node.Status.Allocatable[corev1.ResourceCPU]
			memoryQuantity := node.Status.Allocatable[corev1.ResourceMemory]
			percent := intstr.FromString("28%")
			cpu, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests := corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			rsConfig := pauseRSConfig{
				Replicas: int32(2),
				PodConfig: pausePodConfig{
					Name:      "request-resource-with-single-numa-pod",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						"test-app": "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				},
			}
			runPauseRS(f, rsConfig)

			ginkgo.By("Check the two pods allocated in two NUMA Nodes")
			podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err)
			nodes := sets.NewInt()
			for i := range podList.Items {
				pod := &podList.Items[i]
				ginkgo.By(fmt.Sprintf("pod %q, resourceStatus: %s", pod.Name, pod.Annotations[extension.AnnotationResourceStatus]))
				resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
				framework.ExpectNoError(err, "invalid resourceStatus")
				gomega.Expect(len(resourceStatus.NUMANodeResources)).Should(gomega.Equal(1))
				r := equality.Semantic.DeepEqual(resourceStatus.NUMANodeResources[0].Resources, requests)
				gomega.Expect(r).Should(gomega.Equal(true))
				gomega.Expect(false).Should(gomega.Equal(nodes.Has(int(resourceStatus.NUMANodeResources[0].Node))))
				nodes.Insert(int(resourceStatus.NUMANodeResources[0].Node))
			}
			gomega.Expect(nodes.Len()).Should(gomega.Equal(2))

			ginkgo.By("Create the third LSR Pod allocates 30% resources of Node, expect it failed to schedule")
			percent = intstr.FromString("30%")
			cpu, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			cpu -= cpu % 1000
			memory, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests = corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			pod := createPausePod(f, pausePodConfig{
				Name:      "must-be-failed-pod",
				Namespace: f.Namespace.Name,
				Labels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSLSR),
				},
				PriorityClassName: string(extension.PriorityProd),
				Resources: &corev1.ResourceRequirements{
					Limits:   requests,
					Requests: requests,
				},
				SchedulerName: koordSchedulerName,
				NodeName:      node.Name,
			})
			ginkgo.By("Wait for Pod schedule failed")
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				if scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse &&
					strings.Contains(scheduledCondition.Message, "NUMA Topology affinity") {
					return true, nil
				}
				return false, nil
			}))
		})

		// SingleNUMANode with 2 NUMA Nodes - resources crossover two NUMA Nodes
		framework.ConformanceIt("SingleNUMANode with 2 NUMA Nodes - resources crossover two NUMA Nodes", func() {
			nrt := getSuitableNodeResourceTopology(nrtClient, 2)
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nrt.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node")

			ginkgo.By(fmt.Sprintf("Label node %s with %s=%s", node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode)))
			framework.AddOrUpdateLabelOnNode(f.ClientSet, node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode))
			targetNodeName = node.Name

			ginkgo.By("Create two pods allocate 40% cpu of one NUMA Node, and 40% memory of one NUMA Node")
			cpuQuantity := node.Status.Allocatable[corev1.ResourceCPU]
			memoryQuantity := node.Status.Allocatable[corev1.ResourceMemory]
			percent := intstr.FromString("40%")
			cpu, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			for i := 0; i < 2; i++ {
				requests := corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
				}
				runPausePod(f, pausePodConfig{
					Name:      fmt.Sprintf("request-memory-with-single-numa-pod-%d", i),
					Namespace: f.Namespace.Name,
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				})
			}
			for i := 0; i < 2; i++ {
				requests := corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				}
				runPausePod(f, pausePodConfig{
					Name:      fmt.Sprintf("request-cpu-with-single-numa-pod-%d", i),
					Namespace: f.Namespace.Name,
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				})
			}
			framework.ExpectNoError(e2epod.DeletePodWithWaitByName(f.ClientSet, "request-memory-with-single-numa-pod-0", f.Namespace.Name))
			framework.ExpectNoError(e2epod.DeletePodWithWaitByName(f.ClientSet, "request-cpu-with-single-numa-pod-1", f.Namespace.Name))

			ginkgo.By("Create the third Pod allocates 40% resources of Node, expect it failed to schedule")
			percent = intstr.FromString("40%")
			cpu, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests := corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			pod := createPausePod(f, pausePodConfig{
				Name:      "must-be-failed-pod",
				Namespace: f.Namespace.Name,
				Resources: &corev1.ResourceRequirements{
					Limits:   requests,
					Requests: requests,
				},
				SchedulerName: koordSchedulerName,
				NodeName:      node.Name,
			})
			ginkgo.By("Wait for Pod schedule failed")
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				if scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse &&
					strings.Contains(scheduledCondition.Message, "NUMA Topology affinity") {
					return true, nil
				}
				return false, nil
			}))
		})

		// Restricted with 2 NUMA Nodes
		framework.ConformanceIt("Restricted with 2 NUMA Nodes", func() {
			nrt := getSuitableNodeResourceTopology(nrtClient, 2)
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nrt.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node")

			ginkgo.By(fmt.Sprintf("Label node %s with %s=%s", node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicyRestricted)))
			framework.AddOrUpdateLabelOnNode(f.ClientSet, node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicyRestricted))
			targetNodeName = node.Name

			ginkgo.By("Create two pods allocate 60% resources of Node, and every Pod allocates 30% resources per NUMA Node")
			cpuQuantity := node.Status.Allocatable[corev1.ResourceCPU]
			memoryQuantity := node.Status.Allocatable[corev1.ResourceMemory]
			percent := intstr.FromString("28%")
			cpu, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests := corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			rsConfig := pauseRSConfig{
				Replicas: int32(2),
				PodConfig: pausePodConfig{
					Name:      "request-resource-with-single-numa-pod",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						"test-app": "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				},
			}
			runPauseRS(f, rsConfig)

			ginkgo.By("Check the two pods allocated in two NUMA Nodes")
			podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err)
			nodes := sets.NewInt()
			for i := range podList.Items {
				pod := &podList.Items[i]
				resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
				framework.ExpectNoError(err, "invalid resourceStatus")
				gomega.Expect(len(resourceStatus.NUMANodeResources)).Should(gomega.Equal(1))
				r := equality.Semantic.DeepEqual(resourceStatus.NUMANodeResources[0].Resources, requests)
				gomega.Expect(r).Should(gomega.Equal(true))
				gomega.Expect(false).Should(gomega.Equal(nodes.Has(int(resourceStatus.NUMANodeResources[0].Node))))
				nodes.Insert(int(resourceStatus.NUMANodeResources[0].Node))
			}
			gomega.Expect(nodes.Len()).Should(gomega.Equal(2))

			ginkgo.By("Create the third Pod allocates 40% resources of Node, expect it scheduled")
			percent = intstr.FromString("30%")
			cpu, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests = corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			pod := runPausePod(f, pausePodConfig{
				Name:      "must-be-succeeded-pod",
				Namespace: f.Namespace.Name,
				Resources: &corev1.ResourceRequirements{
					Limits:   requests,
					Requests: requests,
				},
				SchedulerName: koordSchedulerName,
				NodeName:      node.Name,
			})
			nodes = sets.NewInt()
			resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
			framework.ExpectNoError(err, "invalid resourceStatus")
			gomega.Expect(len(resourceStatus.NUMANodeResources)).Should(gomega.Equal(2))
			allocated := quotav1.Add(resourceStatus.NUMANodeResources[0].Resources, resourceStatus.NUMANodeResources[1].Resources)
			r := equality.Semantic.DeepEqual(allocated, requests)
			gomega.Expect(r).Should(gomega.Equal(true))
			nodes.Insert(int(resourceStatus.NUMANodeResources[0].Node))
			nodes.Insert(int(resourceStatus.NUMANodeResources[1].Node))
			gomega.Expect(nodes.Len()).Should(gomega.Equal(2))
		})

		// Restricted with 2 NUMA Nodes - resources crossover two NUMA Nodes
		framework.ConformanceIt("Restricted with 2 NUMA Nodes - resources crossover two NUMA Nodes", func() {
			nrt := getSuitableNodeResourceTopology(nrtClient, 2)
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nrt.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node")

			ginkgo.By(fmt.Sprintf("Label node %s with %s=%s", node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicyRestricted)))
			framework.AddOrUpdateLabelOnNode(f.ClientSet, node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicyRestricted))
			targetNodeName = node.Name

			ginkgo.By("Create two pods allocate 40% cpu of one NUMA Node, and 40% memory of one NUMA Node")
			cpuQuantity := node.Status.Allocatable[corev1.ResourceCPU]
			memoryQuantity := node.Status.Allocatable[corev1.ResourceMemory]
			percent := intstr.FromString("40%")
			cpu, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			for i := 0; i < 2; i++ {
				requests := corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
				}
				runPausePod(f, pausePodConfig{
					Name:      fmt.Sprintf("request-memory-with-single-numa-pod-%d", i),
					Namespace: f.Namespace.Name,
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				})
			}
			for i := 0; i < 2; i++ {
				requests := corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				}
				runPausePod(f, pausePodConfig{
					Name:      fmt.Sprintf("request-cpu-with-single-numa-pod-%d", i),
					Namespace: f.Namespace.Name,
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				})
			}
			framework.ExpectNoError(e2epod.DeletePodWithWaitByName(f.ClientSet, "request-memory-with-single-numa-pod-0", f.Namespace.Name))
			framework.ExpectNoError(e2epod.DeletePodWithWaitByName(f.ClientSet, "request-cpu-with-single-numa-pod-1", f.Namespace.Name))

			ginkgo.By("Create the third Pod allocates 40% resources of Node, expect it scheduled")
			percent = intstr.FromString("40%")
			cpu, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests := corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			pod := runPausePod(f, pausePodConfig{
				Name:      "must-be-failed-pod",
				Namespace: f.Namespace.Name,
				Resources: &corev1.ResourceRequirements{
					Limits:   requests,
					Requests: requests,
				},
				SchedulerName: koordSchedulerName,
				NodeName:      node.Name,
			})
			nodes := sets.NewInt()
			resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
			framework.ExpectNoError(err, "invalid resourceStatus")
			gomega.Expect(len(resourceStatus.NUMANodeResources)).Should(gomega.Equal(2))
			allocated := quotav1.Add(resourceStatus.NUMANodeResources[0].Resources, resourceStatus.NUMANodeResources[1].Resources)
			r := equality.Semantic.DeepEqual(allocated, requests)
			gomega.Expect(r).Should(gomega.Equal(true))
			nodes.Insert(int(resourceStatus.NUMANodeResources[0].Node))
			nodes.Insert(int(resourceStatus.NUMANodeResources[1].Node))
			gomega.Expect(nodes.Len()).Should(gomega.Equal(2))
		})

		// BestEffort with 2 NUMA Nodes
		framework.ConformanceIt("BestEffort with 2 NUMA Nodes", func() {
			nrt := getSuitableNodeResourceTopology(nrtClient, 2)
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nrt.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node")

			ginkgo.By(fmt.Sprintf("Label node %s with %s=%s", node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicyBestEffort)))
			framework.AddOrUpdateLabelOnNode(f.ClientSet, node.Name, extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicyBestEffort))
			targetNodeName = node.Name

			ginkgo.By("Create two pods allocate 60% resources of Node, and every Pod allocates 30% resources per NUMA Node")
			cpuQuantity := node.Status.Allocatable[corev1.ResourceCPU]
			memoryQuantity := node.Status.Allocatable[corev1.ResourceMemory]
			percent := intstr.FromString("28%")
			cpu, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ := intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests := corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			rsConfig := pauseRSConfig{
				Replicas: int32(2),
				PodConfig: pausePodConfig{
					Name:      "request-resource-with-single-numa-pod",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						"test-app": "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: koordSchedulerName,
					NodeName:      node.Name,
				},
			}
			runPauseRS(f, rsConfig)

			ginkgo.By("Check the two pods allocated in two NUMA Nodes")
			podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err)
			nodes := sets.NewInt()
			for i := range podList.Items {
				pod := &podList.Items[i]
				resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
				framework.ExpectNoError(err, "invalid resourceStatus")
				gomega.Expect(len(resourceStatus.NUMANodeResources)).Should(gomega.Equal(1))
				r := equality.Semantic.DeepEqual(resourceStatus.NUMANodeResources[0].Resources, requests)
				gomega.Expect(r).Should(gomega.Equal(true))
				gomega.Expect(false).Should(gomega.Equal(nodes.Has(int(resourceStatus.NUMANodeResources[0].Node))))
				nodes.Insert(int(resourceStatus.NUMANodeResources[0].Node))
			}
			gomega.Expect(nodes.Len()).Should(gomega.Equal(2))

			ginkgo.By("Create the third Pod allocates 40% resources of Node, expect it failed to schedule")
			percent = intstr.FromString("30%")
			cpu, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(cpuQuantity.MilliValue()), false)
			memory, _ = intstr.GetScaledValueFromIntOrPercent(&percent, int(memoryQuantity.Value()), false)
			requests = corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpu), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memory), resource.DecimalSI),
			}
			pod := runPausePod(f, pausePodConfig{
				Name:      "must-be-succeeded-pod",
				Namespace: f.Namespace.Name,
				Resources: &corev1.ResourceRequirements{
					Limits:   requests,
					Requests: requests,
				},
				SchedulerName: koordSchedulerName,
				NodeName:      node.Name,
			})
			nodes = sets.NewInt()
			resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
			framework.ExpectNoError(err, "invalid resourceStatus")
			gomega.Expect(len(resourceStatus.NUMANodeResources)).Should(gomega.Equal(2))
			allocated := quotav1.Add(resourceStatus.NUMANodeResources[0].Resources, resourceStatus.NUMANodeResources[1].Resources)
			r := equality.Semantic.DeepEqual(allocated, requests)
			gomega.Expect(r).Should(gomega.Equal(true))
			nodes.Insert(int(resourceStatus.NUMANodeResources[0].Node))
			nodes.Insert(int(resourceStatus.NUMANodeResources[1].Node))
			gomega.Expect(nodes.Len()).Should(gomega.Equal(2))
		})
	})
})
