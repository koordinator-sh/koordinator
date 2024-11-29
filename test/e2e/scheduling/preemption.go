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
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("Preemption", func() {
	f := framework.NewDefaultFramework("preemption")
	var koordSchedulerName string

	ginkgo.BeforeEach(func() {
		framework.AllNodesReady(f.ClientSet, time.Minute)
		koordSchedulerName = framework.TestContext.KoordSchedulerName
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

	framework.KoordinatorDescribe("Preemption with device", func() {
		framework.ConformanceIt("basic preempt device", func() {
			nodeName := runPodAndGetNodeName(f, pausePodConfig{
				Name: "without-label",
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						apiext.ResourceGPU: resource.MustParse("100"),
					},
					Limits: corev1.ResourceList{
						apiext.ResourceGPU: resource.MustParse("100"),
					},
				},
				SchedulerName: koordSchedulerName,
			})

			ginkgo.By("Create low priority Pod requests all GPUs")
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
			framework.ExpectNoError(err, fmt.Sprintf("unable to get node %v", nodeName))

			lowPriorityPod := createPausePod(f, pausePodConfig{
				Name: "low-priority-pod",
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
					Requests: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
				},
				NodeName:      nodeName,
				SchedulerName: koordSchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, lowPriorityPod), "unable schedule the lowest priority pod")

			ginkgo.By("Create highest priority Pod preempt lowest priority pod to obtain GPUs")
			highPriorityPod := createPausePod(f, pausePodConfig{
				Name: "high-priority-pod",
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
					Requests: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
				},
				NodeName:          nodeName,
				SchedulerName:     koordSchedulerName,
				PriorityClassName: "system-cluster-critical",
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, highPriorityPod), "unable preempt lowest priority pod")
		})

		framework.ConformanceIt("pods outside Reservation cannot preempt pods in Reservation", func() {
			nodeName := runPodAndGetNodeName(f, pausePodConfig{
				Name: "without-label",
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						apiext.ResourceGPU: resource.MustParse("100"),
					},
					Limits: corev1.ResourceList{
						apiext.ResourceGPU: resource.MustParse("100"),
					},
				},
				SchedulerName: koordSchedulerName,
			})
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
			framework.ExpectNoError(err, fmt.Sprintf("unable to get node %v", nodeName))

			ginkgo.By("Create Reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")
			reservation.Spec.AllocateOnce = pointer.Bool(false)
			reservation.Spec.Template.Spec.NodeName = nodeName
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation-preempt": "true",
						},
					},
				},
			}
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
						},
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
						},
					},
				},
			}
			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")
			waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create low priority Pod requests all GPUs")
			lowPriorityPod := createPausePod(f, pausePodConfig{
				Name: "low-priority-pod",
				Labels: map[string]string{
					"test-reservation-preempt": "true",
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
					Requests: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
				},
				NodeName:      nodeName,
				SchedulerName: koordSchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, lowPriorityPod), "unable schedule the lowest priority pod")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, lowPriorityPod.Namespace, lowPriorityPod.Name, reservation.Name)

			ginkgo.By("Create highest priority Pod preempt lowest priority pod to obtain GPUs")
			highPriorityPod := createPausePod(f, pausePodConfig{
				Name: "high-priority-pod",
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
					Requests: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
				},
				NodeName:          nodeName,
				SchedulerName:     koordSchedulerName,
				PriorityClassName: "system-cluster-critical",
			})
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, highPriorityPod.Namespace, highPriorityPod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
			}))

			pod, err := f.PodClient().Get(context.TODO(), lowPriorityPod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			framework.ExpectEqual(pod.DeletionTimestamp, (*metav1.Time)(nil))
		})

		framework.ConformanceIt("highest priority pods in Reservation preempt lowest priority pods in Reservation", func() {
			nodeName := runPodAndGetNodeName(f, pausePodConfig{
				Name: "without-label",
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						apiext.ResourceGPU: resource.MustParse("100"),
					},
					Limits: corev1.ResourceList{
						apiext.ResourceGPU: resource.MustParse("100"),
					},
				},
				SchedulerName: koordSchedulerName,
			})
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
			framework.ExpectNoError(err, fmt.Sprintf("unable to get node %v", nodeName))

			ginkgo.By("Create Reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")
			reservation.Spec.AllocateOnce = pointer.Bool(false)
			reservation.Spec.Template.Spec.NodeName = nodeName
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation-preempt": "true",
						},
					},
				},
			}
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
						},
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
						},
					},
				},
			}
			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")
			waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create low priority Pod requests all GPUs")
			lowPriorityPod := createPausePod(f, pausePodConfig{
				Name: "low-priority-pod",
				Labels: map[string]string{
					"test-reservation-preempt": "true",
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
					Requests: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
				},
				NodeName:      nodeName,
				SchedulerName: koordSchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, lowPriorityPod), "unable schedule the lowest priority pod")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, lowPriorityPod.Namespace, lowPriorityPod.Name, reservation.Name)

			ginkgo.By("Create highest priority Pod preempt lowest priority pod to obtain GPUs")
			highPriorityPod := createPausePod(f, pausePodConfig{
				Name: "high-priority-pod",
				Labels: map[string]string{
					"test-reservation-preempt": "true",
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
					Requests: corev1.ResourceList{
						apiext.ResourceGPU: node.Status.Allocatable[apiext.ResourceGPU],
					},
				},
				NodeName:          nodeName,
				SchedulerName:     koordSchedulerName,
				PriorityClassName: "system-cluster-critical",
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, highPriorityPod), "unable to preempt")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, highPriorityPod.Namespace, highPriorityPod.Name, reservation.Name)
		})
	})

	ginkgo.Context("Preempt basic resources", func() {
		var testNodeName string
		var fakeResourceName corev1.ResourceName = "koordinator.sh/fake-resource"

		ginkgo.BeforeEach(func() {
			ginkgo.By("Add fake resource")
			// find a node which can run a pod:
			testNodeName = GetNodeThatCanRunPod(f)

			// Get node object:
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), testNodeName, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node object for node %v", testNodeName)

			// update Node API object with a fake resource
			nodeCopy := node.DeepCopy()
			nodeCopy.ResourceVersion = "0"

			nodeCopy.Status.Capacity[fakeResourceName] = resource.MustParse("1000")
			nodeCopy.Status.Allocatable[fakeResourceName] = resource.MustParse("1000")
			_, err = f.ClientSet.CoreV1().Nodes().UpdateStatus(context.TODO(), nodeCopy, metav1.UpdateOptions{})
			framework.ExpectNoError(err, "unable to apply fake resource to %v", testNodeName)
		})

		ginkgo.AfterEach(func() {
			ginkgo.By("Remove fake resource")
			// remove fake resource:
			if testNodeName != "" {
				node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), testNodeName, metav1.GetOptions{})
				framework.ExpectNoError(err, "unable to get node object for node %v", testNodeName)

				nodeCopy := node.DeepCopy()
				// force it to update
				nodeCopy.ResourceVersion = "0"
				delete(nodeCopy.Status.Capacity, fakeResourceName)
				delete(nodeCopy.Status.Allocatable, fakeResourceName)
				_, err = f.ClientSet.CoreV1().Nodes().UpdateStatus(context.TODO(), nodeCopy, metav1.UpdateOptions{})
				framework.ExpectNoError(err, "unable to update node %v", testNodeName)
			}
		})

		framework.ConformanceIt("basic preempt", func() {
			resourceRequirements := &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					fakeResourceName:   resource.MustParse("1000"),
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				Limits: corev1.ResourceList{
					fakeResourceName:   resource.MustParse("1000"),
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			}
			nodeName := runPodAndGetNodeName(f, pausePodConfig{
				Name:          "without-label",
				Resources:     resourceRequirements,
				SchedulerName: koordSchedulerName,
			})

			ginkgo.By("Create low priority Pod requests all fakeResource")

			lowPriorityPod := createPausePod(f, pausePodConfig{
				Name:          "low-priority-pod",
				Resources:     resourceRequirements,
				NodeName:      nodeName,
				SchedulerName: koordSchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, lowPriorityPod), "unable schedule the lowest priority pod")

			ginkgo.By("Create highest priority Pod preempt lowest priority pod to obtain fakeResource")
			highPriorityPod := createPausePod(f, pausePodConfig{
				Name:              "high-priority-pod",
				Resources:         resourceRequirements,
				NodeName:          nodeName,
				SchedulerName:     koordSchedulerName,
				PriorityClassName: "system-cluster-critical",
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, highPriorityPod), "unable preempt lowest priority pod")
		})

		framework.ConformanceIt("pods outside Reservation cannot preempt pods in Reservation", func() {
			resourceRequirements := &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					fakeResourceName:   resource.MustParse("1000"),
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				Limits: corev1.ResourceList{
					fakeResourceName:   resource.MustParse("1000"),
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			}

			ginkgo.By("Create Reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")
			reservation.Spec.AllocateOnce = pointer.Bool(false)
			reservation.Spec.Template.Spec.NodeName = testNodeName
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation-preempt": "true",
						},
					},
				},
			}
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
						Limits: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
					},
				},
			}
			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")
			waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create low priority Pod requests all fakeResource")
			lowPriorityPod := createPausePod(f, pausePodConfig{
				Name: "low-priority-pod",
				Labels: map[string]string{
					"test-reservation-preempt": "true",
				},
				Resources:     resourceRequirements,
				NodeName:      testNodeName,
				SchedulerName: koordSchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, lowPriorityPod), "unable schedule the lowest priority pod")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, lowPriorityPod.Namespace, lowPriorityPod.Name, reservation.Name)

			ginkgo.By("Create highest priority Pod preempt lowest priority pod to obtain fakeResource")
			highPriorityPod := createPausePod(f, pausePodConfig{
				Name:              "high-priority-pod",
				Resources:         resourceRequirements,
				NodeName:          testNodeName,
				SchedulerName:     koordSchedulerName,
				PriorityClassName: "system-cluster-critical",
			})
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, highPriorityPod.Namespace, highPriorityPod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
			}))

			pod, err := f.PodClient().Get(context.TODO(), lowPriorityPod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			framework.ExpectEqual(pod.DeletionTimestamp, (*metav1.Time)(nil))
		})

		framework.ConformanceIt("highest priority pods in Reservation preempt lowest priority pods in Reservation", func() {
			resourceRequirements := &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					fakeResourceName:   resource.MustParse("1000"),
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				Limits: corev1.ResourceList{
					fakeResourceName:   resource.MustParse("1000"),
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			}

			ginkgo.By("Create Reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")
			reservation.Spec.AllocateOnce = pointer.Bool(false)
			reservation.Spec.Template.Spec.NodeName = testNodeName
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation-preempt": "true",
						},
					},
				},
			}
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
						Limits: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
					},
				},
			}
			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")
			waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create low priority Pod requests all fakeResource")
			lowPriorityPod := createPausePod(f, pausePodConfig{
				Name: "low-priority-pod",
				Labels: map[string]string{
					"test-reservation-preempt": "true",
				},
				Resources:     resourceRequirements,
				NodeName:      testNodeName,
				SchedulerName: koordSchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, lowPriorityPod), "unable schedule the lowest priority pod")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, lowPriorityPod.Namespace, lowPriorityPod.Name, reservation.Name)

			ginkgo.By("Create highest priority Pod preempt lowest priority pod to obtain fakeResource")
			highPriorityPod := createPausePod(f, pausePodConfig{
				Name: "high-priority-pod",
				Labels: map[string]string{
					"test-reservation-preempt": "true",
				},
				Resources:         resourceRequirements,
				NodeName:          testNodeName,
				SchedulerName:     koordSchedulerName,
				PriorityClassName: "system-cluster-critical",
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, highPriorityPod), "unable to preempt")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, highPriorityPod.Namespace, highPriorityPod.Name, reservation.Name)
		})

		framework.ConformanceIt("highest priority pods in Restricted Reservation preempt lowest priority pods in Restricted Reservation", func() {
			resourceRequirements := &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					fakeResourceName:   resource.MustParse("1000"),
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				Limits: corev1.ResourceList{
					fakeResourceName:   resource.MustParse("1000"),
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			}

			ginkgo.By("Create Reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")
			reservation.Spec.AllocateOnce = pointer.Bool(false)
			reservation.Spec.Template.Spec.NodeName = testNodeName
			reservation.Spec.AllocatePolicy = schedulingv1alpha1.ReservationAllocatePolicyRestricted
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation-preempt": "true",
						},
					},
				},
			}
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
						Limits: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
					},
				},
			}
			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")
			waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create low priority Pod requests all fakeResource")
			lowPriorityPod := createPausePod(f, pausePodConfig{
				Name: "low-priority-pod",
				Labels: map[string]string{
					"test-reservation-preempt": "true",
				},
				Resources:     resourceRequirements,
				NodeName:      testNodeName,
				SchedulerName: koordSchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, lowPriorityPod), "unable schedule the lowest priority pod")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, lowPriorityPod.Namespace, lowPriorityPod.Name, reservation.Name)

			ginkgo.By("Create highest priority Pod preempt lowest priority pod to obtain fakeResource")
			highPriorityPod := createPausePod(f, pausePodConfig{
				Name: "high-priority-pod",
				Labels: map[string]string{
					"test-reservation-preempt": "true",
				},
				Resources:         resourceRequirements,
				NodeName:          testNodeName,
				SchedulerName:     koordSchedulerName,
				PriorityClassName: "system-cluster-critical",
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, highPriorityPod), "unable to preempt")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, highPriorityPod.Namespace, highPriorityPod.Name, reservation.Name)
		})
	})
})
