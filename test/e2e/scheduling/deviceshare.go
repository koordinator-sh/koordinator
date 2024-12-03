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
	"sort"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("DeviceShare", func() {
	f := framework.NewDefaultFramework("deviceshare")

	ginkgo.BeforeEach(func() {
		framework.AllNodesReady(f.ClientSet, time.Minute)
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

	framework.KoordinatorDescribe("Reserve GPU", func() {
		framework.ConformanceIt("Create Reservation disables AllocateOnce, reserves 50% resource of a GPU instance, only one Pod of all matched reservation that is using reservation ", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			targetPodLabel := "test-reserve-gpu"
			reservation.Spec.AllocateOnce = pointer.Bool(false)
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							targetPodLabel: "true",
						},
					},
				},
			}
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: resource.MustParse("50"),
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Get Reserved node")
			nodeName := reservation.Status.NodeName
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node object for node %v", nodeName)

			totalGPU := node.Status.Allocatable[apiext.ResourceGPU]
			numPods := totalGPU.Value() / 50

			ginkgo.By(fmt.Sprintf("Create %d pods", numPods))
			requests := corev1.ResourceList{
				apiext.ResourceGPU: resource.MustParse("50"),
			}
			replicas := numPods
			rsConfig := pauseRSConfig{
				Replicas: int32(replicas),
				PodConfig: pausePodConfig{
					Name:      targetPodLabel,
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				},
			}
			runPauseRS(f, rsConfig)
			podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("Check pods and reservation status")
			reservedCount := 0
			var podUsingReservation *corev1.Pod
			for i := range podList.Items {
				pod := &podList.Items[i]
				reservationAllocated, err := apiext.GetReservationAllocated(pod)
				framework.ExpectNoError(err)
				if reservationAllocated != nil {
					gomega.Expect(reservationAllocated).Should(gomega.Equal(&apiext.ReservationAllocated{
						Name: reservation.Name,
						UID:  reservation.UID,
					}), "pod is not using the expected reservation")
					podDeviceAllocations, err := apiext.GetDeviceAllocations(pod.Annotations)
					framework.ExpectNoError(err)
					reservationDeviceAllocations, err := apiext.GetDeviceAllocations(reservation.Annotations)
					framework.ExpectNoError(err)
					gomega.Expect(podDeviceAllocations).Should(gomega.Equal(reservationDeviceAllocations), "unexpected device allocations")
					reservedCount++
					podUsingReservation = pod
				}
			}
			gomega.Expect(reservedCount).Should(gomega.Equal(1), "no pods using the expected reservation")

			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			reservationRequests := reservationutil.ReservationRequests(reservation)
			gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

			podRequests := resourceapi.PodRequests(podUsingReservation, resourceapi.PodResourcesOptions{})
			podRequests = quotav1.Mask(podRequests, quotav1.ResourceNames(reservation.Status.Allocatable))
			gomega.Expect(reservation.Status.Allocated).Should(gomega.Equal(podRequests))
			gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal([]corev1.ObjectReference{
				{
					Namespace: podUsingReservation.Namespace,
					Name:      podUsingReservation.Name,
					UID:       podUsingReservation.UID,
				},
			}), "reservation.status.currentOwners is not as expected")
		})

		framework.ConformanceIt("Create Reservation disables AllocateOnce, reserves 50% resource of a GPU instance, and one Pod matched reservation, other pods unmatched reservation", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			targetPodLabel := "test-reserve-gpu"
			reservation.Spec.AllocateOnce = pointer.Bool(false)
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							targetPodLabel: "true",
						},
					},
				},
			}
			reservation.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: resource.MustParse("50"),
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create 1 pods that matched reservation")
			requests := corev1.ResourceList{
				apiext.ResourceGPU: resource.MustParse("50"),
			}
			rsConfig := pauseRSConfig{
				Replicas: int32(1),
				PodConfig: pausePodConfig{
					Name:      targetPodLabel,
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				},
			}
			runPauseRS(f, rsConfig)

			ginkgo.By("Get Reserved node")
			nodeName := reservation.Status.NodeName
			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node object for node %v", nodeName)

			totalGPU := node.Status.Allocatable[apiext.ResourceGPU]
			numPods := totalGPU.Value()/50 - 1

			ginkgo.By(fmt.Sprintf("Create %d pods that unmatched reservation", numPods))
			replicas := numPods
			rsConfig = pauseRSConfig{
				Replicas: int32(replicas),
				PodConfig: pausePodConfig{
					Name:      "test-reserve-gpu-unmatched",
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						"test-reserve-gpu-unmatched": "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				},
			}
			runPauseRS(f, rsConfig)
			podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("Check pods and reservation status")
			reservedCount := 0
			var podUsingReservation *corev1.Pod
			for i := range podList.Items {
				pod := &podList.Items[i]
				reservationAllocated, err := apiext.GetReservationAllocated(pod)
				framework.ExpectNoError(err)
				if reservationAllocated != nil {
					gomega.Expect(reservationAllocated).Should(gomega.Equal(&apiext.ReservationAllocated{
						Name: reservation.Name,
						UID:  reservation.UID,
					}), "pod is not using the expected reservation")
					podDeviceAllocations, err := apiext.GetDeviceAllocations(pod.Annotations)
					framework.ExpectNoError(err)
					reservationDeviceAllocations, err := apiext.GetDeviceAllocations(reservation.Annotations)
					framework.ExpectNoError(err)
					gomega.Expect(podDeviceAllocations).Should(gomega.Equal(reservationDeviceAllocations), "unexpected device allocations")
					reservedCount++
					podUsingReservation = pod
				}
			}
			gomega.Expect(reservedCount).Should(gomega.Equal(1), "no pods using the expected reservation")

			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			reservationRequests := reservationutil.ReservationRequests(reservation)
			gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

			podRequests := resourceapi.PodRequests(podUsingReservation, resourceapi.PodResourcesOptions{})
			podRequests = quotav1.Mask(podRequests, quotav1.ResourceNames(reservation.Status.Allocatable))
			gomega.Expect(reservation.Status.Allocated).Should(gomega.Equal(podRequests))
			gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal([]corev1.ObjectReference{
				{
					Namespace: podUsingReservation.Namespace,
					Name:      podUsingReservation.Name,
					UID:       podUsingReservation.UID,
				},
			}), "reservation.status.currentOwners is not as expected")
		})

		framework.ConformanceIt(
			"Create Reservation disables AllocateOnce with Restricted Policy, "+
				"reserves 50% resource of a GPU instance, and 2 Pods matched reservation but only 1 Pod scheduled, "+
				"other 1 Pod failed scheduling",
			func() {
				ginkgo.By("Create reservation")
				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable to load reservation")

				targetPodLabel := "test-reserve-gpu"
				reservation.Spec.AllocateOnce = pointer.Bool(false)
				reservation.Spec.AllocatePolicy = schedulingv1alpha1.ReservationAllocatePolicyRestricted
				reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								targetPodLabel: "true",
							},
						},
					},
				}
				reservation.Spec.Template.Spec.Containers = []corev1.Container{
					{
						Name: "main",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								apiext.ResourceGPU: resource.MustParse("50"),
							},
						},
					},
				}

				_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				framework.ExpectNoError(err, "unable to create reservation")

				ginkgo.By("Wait for reservation scheduled")
				reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

				ginkgo.By("Create 1 pods allocated from reservation")
				requests := corev1.ResourceList{
					apiext.ResourceGPU: resource.MustParse("50"),
				}
				rsConfig := pauseRSConfig{
					Replicas: int32(1),
					PodConfig: pausePodConfig{
						Name:      targetPodLabel,
						Namespace: f.Namespace.Name,
						Labels: map[string]string{
							"success":      "true",
							targetPodLabel: "true",
						},
						Resources: &corev1.ResourceRequirements{
							Limits:   requests,
							Requests: requests,
						},
						SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
					},
				}
				runPauseRS(f, rsConfig)

				ginkgo.By("Create other one Pod also matched the reservation")
				pod := createPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					NodeName:      reservation.Status.NodeName,
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				})

				ginkgo.By("Wait for Pod schedule failed")
				framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
					_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
					return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
				}))

				podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				framework.ExpectNoError(err)

				ginkgo.By("Check pods and reservation status")
				reservedCount := 0
				var podUsingReservation *corev1.Pod
				for i := range podList.Items {
					pod := &podList.Items[i]
					reservationAllocated, err := apiext.GetReservationAllocated(pod)
					framework.ExpectNoError(err)
					if reservationAllocated != nil {
						gomega.Expect(reservationAllocated).Should(gomega.Equal(&apiext.ReservationAllocated{
							Name: reservation.Name,
							UID:  reservation.UID,
						}), "pod is not using the expected reservation")
						podDeviceAllocations, err := apiext.GetDeviceAllocations(pod.Annotations)
						framework.ExpectNoError(err)
						reservationDeviceAllocations, err := apiext.GetDeviceAllocations(reservation.Annotations)
						framework.ExpectNoError(err)
						gomega.Expect(podDeviceAllocations).Should(gomega.Equal(reservationDeviceAllocations), "unexpected device allocations")
						reservedCount++
						podUsingReservation = pod
					}
				}
				gomega.Expect(reservedCount).Should(gomega.Equal(1), "no pods using the expected reservation")

				reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				reservationRequests := reservationutil.ReservationRequests(reservation)
				gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

				podRequests := resourceapi.PodRequests(podUsingReservation, resourceapi.PodResourcesOptions{})
				podRequests = quotav1.Mask(podRequests, quotav1.ResourceNames(reservation.Status.Allocatable))
				gomega.Expect(reservation.Status.Allocated).Should(gomega.Equal(podRequests))
				gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal([]corev1.ObjectReference{
					{
						Namespace: podUsingReservation.Namespace,
						Name:      podUsingReservation.Name,
						UID:       podUsingReservation.UID,
					},
				}), "reservation.status.currentOwners is not as expected")
			},
		)

		framework.ConformanceIt(
			"Create Reservation disables AllocateOnce with Restricted Policy, reserves 2 GPU instances, "+
				"and creates 3 Pods with 60% GPU, but one pod failed",
			func() {
				ginkgo.By("Create reservation")
				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable to load reservation")

				targetPodLabel := "test-reserve-gpu"
				reservation.Spec.AllocateOnce = pointer.Bool(false)
				reservation.Spec.AllocatePolicy = schedulingv1alpha1.ReservationAllocatePolicyRestricted
				reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								targetPodLabel: "true",
							},
						},
					},
				}
				reservation.Spec.Template.Spec.Containers = []corev1.Container{
					{
						Name: "main",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								apiext.ResourceGPU: resource.MustParse("200"),
							},
						},
					},
				}

				_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				framework.ExpectNoError(err, "unable to create reservation")

				ginkgo.By("Wait for reservation scheduled")
				reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

				ginkgo.By("Create 2 pods allocated from reservation")
				requests := corev1.ResourceList{
					apiext.ResourceGPU: resource.MustParse("60"),
				}
				rsConfig := pauseRSConfig{
					Replicas: int32(2),
					PodConfig: pausePodConfig{
						Name:      targetPodLabel,
						Namespace: f.Namespace.Name,
						Labels: map[string]string{
							"success":      "true",
							targetPodLabel: "true",
						},
						Resources: &corev1.ResourceRequirements{
							Limits:   requests,
							Requests: requests,
						},
						SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
					},
				}
				runPauseRS(f, rsConfig)

				ginkgo.By("Create the third Pod also matched the reservation")
				pod := createPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					NodeName:      reservation.Status.NodeName,
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				})

				ginkgo.By("Wait for Pod schedule failed")
				framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
					_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
					return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
				}))

				podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				framework.ExpectNoError(err)

				ginkgo.By("Check pods and reservation status")
				reservedCount := 0
				podUsingReservations := make(map[types.UID]*corev1.Pod)
				for i := range podList.Items {
					pod := &podList.Items[i]
					reservationAllocated, err := apiext.GetReservationAllocated(pod)
					framework.ExpectNoError(err)
					if reservationAllocated != nil {
						gomega.Expect(reservationAllocated).Should(gomega.Equal(&apiext.ReservationAllocated{
							Name: reservation.Name,
							UID:  reservation.UID,
						}), "pod is not using the expected reservation")
						podDeviceAllocations, err := apiext.GetDeviceAllocations(pod.Annotations)
						framework.ExpectNoError(err)
						reservationDeviceAllocations, err := apiext.GetDeviceAllocations(reservation.Annotations)
						framework.ExpectNoError(err)

						for deviceType, allocations := range podDeviceAllocations {
							for _, alloc := range allocations {
								found := false
								reservationAllocations := reservationDeviceAllocations[deviceType]
								for _, reservationAlloc := range reservationAllocations {
									if reservationAlloc.Minor == alloc.Minor {
										if fit, _ := quotav1.LessThanOrEqual(alloc.Resources, reservationAlloc.Resources); fit {
											found = true
											break
										}
									}
								}
								gomega.Expect(found).Should(gomega.Equal(true), "unexpected device allocations")
							}
						}
						reservedCount++
						podUsingReservations[pod.UID] = pod
					}
				}
				gomega.Expect(reservedCount).Should(gomega.Equal(2), "no pods using the expected reservation")

				reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				reservationRequests := reservationutil.ReservationRequests(reservation)
				gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

				var totalRequests corev1.ResourceList
				var currentOwners []corev1.ObjectReference
				for _, pod := range podUsingReservations {
					podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
					podRequests = quotav1.Mask(podRequests, quotav1.ResourceNames(reservation.Status.Allocatable))
					totalRequests = quotav1.Add(totalRequests, podRequests)
					currentOwners = append(currentOwners, corev1.ObjectReference{
						Namespace: pod.Namespace,
						Name:      pod.Name,
						UID:       pod.UID,
					})
				}
				sort.Slice(currentOwners, func(i, j int) bool {
					return currentOwners[i].UID < currentOwners[j].UID
				})

				gomega.Expect(equality.Semantic.DeepEqual(reservation.Status.Allocated, totalRequests)).Should(gomega.Equal(true))
				gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal(currentOwners), "reservation.status.currentOwners is not as expected")
			},
		)

		framework.ConformanceIt(
			"Create Reservation disables AllocateOnce with Aligned Policy, reserves 50% resource of a GPU instance,"+
				" and 3 Pods with 40% matched reservation, 2 Pods can allocate from reservation, 1 Pod allocates from node",
			func() {
				ginkgo.By("Create reservation")
				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable to load reservation")

				targetPodLabel := "test-reserve-gpu"
				reservation.Spec.AllocateOnce = pointer.Bool(false)
				reservation.Spec.AllocatePolicy = schedulingv1alpha1.ReservationAllocatePolicyAligned
				reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								targetPodLabel: "true",
							},
						},
					},
				}
				reservation.Spec.Template.Spec.Containers = []corev1.Container{
					{
						Name: "main",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								apiext.ResourceGPU: resource.MustParse("50"),
							},
						},
					},
				}

				_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				framework.ExpectNoError(err, "unable to create reservation")

				ginkgo.By("Wait for reservation scheduled")
				reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

				ginkgo.By("Create 3 pods allocated from reservation")
				requests := corev1.ResourceList{
					apiext.ResourceGPU: resource.MustParse("40"),
				}
				rsConfig := pauseRSConfig{
					Replicas: int32(3),
					PodConfig: pausePodConfig{
						Name:      targetPodLabel,
						Namespace: f.Namespace.Name,
						Labels: map[string]string{
							"success":      "true",
							targetPodLabel: "true",
						},
						Resources: &corev1.ResourceRequirements{
							Limits:   requests,
							Requests: requests,
						},
						SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
					},
				}
				runPauseRS(f, rsConfig)

				podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				framework.ExpectNoError(err)

				ginkgo.By("Check pods and reservation status")
				reservedCount := 0
				podUsingReservations := make(map[types.UID]*corev1.Pod)
				for i := range podList.Items {
					pod := &podList.Items[i]
					reservationAllocated, err := apiext.GetReservationAllocated(pod)
					framework.ExpectNoError(err)
					if reservationAllocated != nil {
						gomega.Expect(reservationAllocated).Should(gomega.Equal(&apiext.ReservationAllocated{
							Name: reservation.Name,
							UID:  reservation.UID,
						}), "pod is not using the expected reservation")
						podDeviceAllocations, err := apiext.GetDeviceAllocations(pod.Annotations)
						framework.ExpectNoError(err)
						reservationDeviceAllocations, err := apiext.GetDeviceAllocations(reservation.Annotations)
						framework.ExpectNoError(err)

						for deviceType, allocations := range podDeviceAllocations {
							for _, alloc := range allocations {
								found := false
								reservationAllocations := reservationDeviceAllocations[deviceType]
								for _, reservationAlloc := range reservationAllocations {
									if reservationAlloc.Minor == alloc.Minor {
										if fit, _ := quotav1.LessThanOrEqual(alloc.Resources, reservationAlloc.Resources); fit {
											found = true
											break
										}
									}
								}
								gomega.Expect(found).Should(gomega.Equal(true), "unexpected device allocations")
							}
						}
						reservedCount++
						podUsingReservations[pod.UID] = pod
					}
				}
				gomega.Expect(reservedCount).Should(gomega.Equal(2), "no pods using the expected reservation")

				reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				reservationRequests := reservationutil.ReservationRequests(reservation)
				gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

				var totalRequests corev1.ResourceList
				var currentOwners []corev1.ObjectReference
				for _, pod := range podUsingReservations {
					podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
					podRequests = quotav1.Mask(podRequests, quotav1.ResourceNames(reservation.Status.Allocatable))
					totalRequests = quotav1.Add(totalRequests, podRequests)
					currentOwners = append(currentOwners, corev1.ObjectReference{
						Namespace: pod.Namespace,
						Name:      pod.Name,
						UID:       pod.UID,
					})
				}
				sort.Slice(currentOwners, func(i, j int) bool {
					return currentOwners[i].UID < currentOwners[j].UID
				})

				gomega.Expect(equality.Semantic.DeepEqual(reservation.Status.Allocated, totalRequests)).Should(gomega.Equal(true))
				gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal(currentOwners), "reservation.status.currentOwners is not as expected")
			},
		)

		framework.ConformanceIt("Create Reservation disables AllocateOnce with Aligned Policy, "+
			"reserves 50% resource of a GPU instance, create 3 Pods to allocate 40% per GPU from node,  "+
			"and 2 Pods with 40% matched reservation, 1 Pod can allocate from reservation, the other 1 Pod failed",
			func() {
				ginkgo.By("Create reservation")
				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable to load reservation")

				targetPodLabel := "test-reserve-gpu"
				reservation.Spec.AllocateOnce = pointer.Bool(false)
				reservation.Spec.AllocatePolicy = schedulingv1alpha1.ReservationAllocatePolicyAligned
				reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								targetPodLabel: "true",
							},
						},
					},
				}
				reservation.Spec.Template.Spec.Containers = []corev1.Container{
					{
						Name: "main",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								apiext.ResourceGPU: resource.MustParse("50"),
							},
						},
					},
				}

				_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				framework.ExpectNoError(err, "unable to create reservation")

				ginkgo.By("Wait for reservation scheduled")
				reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

				ginkgo.By("Create 3 pods allocated from node")
				requests := corev1.ResourceList{
					apiext.ResourceGPU: resource.MustParse("40"),
				}
				rsConfig := pauseRSConfig{
					Replicas: int32(3),
					PodConfig: pausePodConfig{
						Name:      "test-pod",
						Namespace: f.Namespace.Name,
						Labels: map[string]string{
							"success": "true",
							"testPod": "true",
						},
						Resources: &corev1.ResourceRequirements{
							Limits:   requests,
							Requests: requests,
						},
						SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
					},
				}
				runPauseRS(f, rsConfig)

				ginkgo.By("Create 1 pods allocated from reservation")
				rsConfig = pauseRSConfig{
					Replicas: int32(1),
					PodConfig: pausePodConfig{
						Name:      targetPodLabel,
						Namespace: f.Namespace.Name,
						Labels: map[string]string{
							"success":      "true",
							targetPodLabel: "true",
						},
						Resources: &corev1.ResourceRequirements{
							Limits:   requests,
							Requests: requests,
						},
						SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
					},
				}
				runPauseRS(f, rsConfig)

				ginkgo.By("Create 1 Pod also matched the reservation but expect failed scheduling")
				pod := createPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					NodeName:      reservation.Status.NodeName,
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				})

				ginkgo.By("Wait for Pod schedule failed")
				framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
					_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
					return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
				}))

				podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				framework.ExpectNoError(err)

				ginkgo.By("Check pods and reservation status")
				reservedCount := 0
				podUsingReservations := make(map[types.UID]*corev1.Pod)
				for i := range podList.Items {
					pod := &podList.Items[i]
					reservationAllocated, err := apiext.GetReservationAllocated(pod)
					framework.ExpectNoError(err)
					if reservationAllocated != nil {
						gomega.Expect(reservationAllocated).Should(gomega.Equal(&apiext.ReservationAllocated{
							Name: reservation.Name,
							UID:  reservation.UID,
						}), "pod is not using the expected reservation")
						podDeviceAllocations, err := apiext.GetDeviceAllocations(pod.Annotations)
						framework.ExpectNoError(err)
						reservationDeviceAllocations, err := apiext.GetDeviceAllocations(reservation.Annotations)
						framework.ExpectNoError(err)

						for deviceType, allocations := range podDeviceAllocations {
							for _, alloc := range allocations {
								found := false
								reservationAllocations := reservationDeviceAllocations[deviceType]
								for _, reservationAlloc := range reservationAllocations {
									if reservationAlloc.Minor == alloc.Minor {
										if fit, _ := quotav1.LessThanOrEqual(alloc.Resources, reservationAlloc.Resources); fit {
											found = true
											break
										}
									}
								}
								gomega.Expect(found).Should(gomega.Equal(true), "unexpected device allocations")
							}
						}
						reservedCount++
						podUsingReservations[pod.UID] = pod
					}
				}
				gomega.Expect(reservedCount).Should(gomega.Equal(1), "no pods using the expected reservation")

				reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				reservationRequests := reservationutil.ReservationRequests(reservation)
				gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

				var totalRequests corev1.ResourceList
				var currentOwners []corev1.ObjectReference
				for _, pod := range podUsingReservations {
					podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
					podRequests = quotav1.Mask(podRequests, quotav1.ResourceNames(reservation.Status.Allocatable))
					totalRequests = quotav1.Add(totalRequests, podRequests)
					currentOwners = append(currentOwners, corev1.ObjectReference{
						Namespace: pod.Namespace,
						Name:      pod.Name,
						UID:       pod.UID,
					})
				}
				sort.Slice(currentOwners, func(i, j int) bool {
					return currentOwners[i].UID < currentOwners[j].UID
				})

				gomega.Expect(equality.Semantic.DeepEqual(reservation.Status.Allocated, totalRequests)).Should(gomega.Equal(true))
				gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal(currentOwners), "reservation.status.currentOwners is not as expected")
			},
		)

		framework.ConformanceIt("Create 4 Reservations disables AllocateOnce with Aligned Policy, "+
			"reserves 50% resource of a GPU instance, create 5 Pods to allocate 30% per GPU from reservations,  "+
			"and 4 Pods can allocate from reservation, and 1 Pod failed",
			func() {
				ginkgo.By("Create reservation")
				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable to load reservation")

				targetPodLabel := "test-reserve-gpu"
				for i := 0; i < 4; i++ {
					reservation.Name = fmt.Sprintf("algned-reservation-%d", i)
					reservation.Spec.AllocateOnce = pointer.Bool(false)
					reservation.Spec.AllocatePolicy = schedulingv1alpha1.ReservationAllocatePolicyAligned
					reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									targetPodLabel: "true",
								},
							},
						},
					}
					reservation.Spec.Template.Spec.Containers = []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPU: resource.MustParse("50"),
								},
							},
						},
					}
					_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
					framework.ExpectNoError(err, "unable to create reservation")

					ginkgo.By("Wait for reservation scheduled")
					waitingForReservationScheduled(f.KoordinatorClientSet, reservation)
				}

				ginkgo.By("Create 4 pods allocated from reservation")
				requests := corev1.ResourceList{
					apiext.ResourceGPU: resource.MustParse("30"),
				}
				rsConfig := pauseRSConfig{
					Replicas: int32(4),
					PodConfig: pausePodConfig{
						Name:      targetPodLabel,
						Namespace: f.Namespace.Name,
						Labels: map[string]string{
							"success":      "true",
							targetPodLabel: "true",
						},
						Resources: &corev1.ResourceRequirements{
							Limits:   requests,
							Requests: requests,
						},
						SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
					},
				}
				runPauseRS(f, rsConfig)

				ginkgo.By("Create 1 Pod also matched the reservation but expect failed scheduling")
				pod := createPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits:   requests,
						Requests: requests,
					},
					NodeName:      reservation.Status.NodeName,
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				})

				ginkgo.By("Wait for Pod schedule failed")
				framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
					_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
					return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
				}))

				podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				framework.ExpectNoError(err)

				ginkgo.By("Check pods and reservation status")
				reservationNames := map[string]int{}
				for i := range podList.Items {
					pod := &podList.Items[i]
					reservationAllocated, err := apiext.GetReservationAllocated(pod)
					framework.ExpectNoError(err)
					if reservationAllocated == nil {
						continue
					}
					reservationNames[reservationAllocated.Name]++
				}
				gomega.Expect(reservationNames).Should(gomega.Equal(map[string]int{
					"algned-reservation-0": 1,
					"algned-reservation-1": 1,
					"algned-reservation-2": 1,
					"algned-reservation-3": 1,
				}), "unexpected reservation bound states")

				for i := range podList.Items {
					pod := &podList.Items[i]
					reservationAllocated, err := apiext.GetReservationAllocated(pod)
					framework.ExpectNoError(err)
					if reservationAllocated == nil {
						continue
					}
					podDeviceAllocations, err := apiext.GetDeviceAllocations(pod.Annotations)
					framework.ExpectNoError(err)

					reservation, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservationAllocated.Name, metav1.GetOptions{})
					framework.ExpectNoError(err)
					reservationRequests := reservationutil.ReservationRequests(reservation)
					gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

					var totalRequests corev1.ResourceList
					var currentOwners []corev1.ObjectReference
					podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
					podRequests = quotav1.Mask(podRequests, quotav1.ResourceNames(reservation.Status.Allocatable))
					totalRequests = quotav1.Add(totalRequests, podRequests)
					currentOwners = append(currentOwners, corev1.ObjectReference{
						Namespace: pod.Namespace,
						Name:      pod.Name,
						UID:       pod.UID,
					})
					sort.Slice(currentOwners, func(i, j int) bool {
						return currentOwners[i].UID < currentOwners[j].UID
					})
					gomega.Expect(equality.Semantic.DeepEqual(reservation.Status.Allocated, totalRequests)).Should(gomega.Equal(true))
					gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal(currentOwners), "reservation.status.currentOwners is not as expected")

					reservationDeviceAllocations, err := apiext.GetDeviceAllocations(reservation.Annotations)
					framework.ExpectNoError(err)

					for deviceType, allocations := range podDeviceAllocations {
						for _, alloc := range allocations {
							found := false
							reservationAllocations := reservationDeviceAllocations[deviceType]
							for _, reservationAlloc := range reservationAllocations {
								if reservationAlloc.Minor == alloc.Minor {
									if fit, _ := quotav1.LessThanOrEqual(alloc.Resources, reservationAlloc.Resources); fit {
										found = true
										break
									}
								}
							}
							gomega.Expect(found).Should(gomega.Equal(true), "unexpected device allocations")
						}
					}
				}
			},
		)
	})
})
