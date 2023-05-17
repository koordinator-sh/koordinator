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

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
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
			requests := reservationutil.ReservationRequests(reservation)
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
					reservedCount++
					podUsingReservation = pod
				}
			}
			gomega.Expect(reservedCount).Should(gomega.Equal(1), "no pods using the expected reservation")

			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			reservationRequests := reservationutil.ReservationRequests(reservation)
			gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

			podRequests, _ := resourceapi.PodRequestsAndLimits(podUsingReservation)
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
			requests := reservationutil.ReservationRequests(reservation)
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
					reservedCount++
					podUsingReservation = pod
				}
			}
			gomega.Expect(reservedCount).Should(gomega.Equal(1), "no pods using the expected reservation")

			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			reservationRequests := reservationutil.ReservationRequests(reservation)
			gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

			podRequests, _ := resourceapi.PodRequestsAndLimits(podUsingReservation)
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
	})
})
