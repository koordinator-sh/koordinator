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
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/pointer"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("HostPort", func() {
	f := framework.NewDefaultFramework("hostport")

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

	framework.KoordinatorDescribe("HostPort Reservation", func() {
		framework.ConformanceIt("Create Reservation disables AllocateOnce, reserve ports only can be allocated once", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			targetPodLabel := "test-reserve-ports"
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
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "port-54321",
							ContainerPort: 1111,
							HostPort:      54321,
							Protocol:      corev1.ProtocolTCP,
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create one pod allocate port 54321")
			pod := runPausePod(f, pausePodConfig{
				Name:      "allocate-port-54321",
				Namespace: f.Namespace.Name,
				Labels: map[string]string{
					targetPodLabel: "true",
				},
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1"),
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "port-54321",
						ContainerPort: 1111,
						HostPort:      54321,
						Protocol:      corev1.ProtocolTCP,
					},
				},
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})

			ginkgo.By("Create one pod allocate port 54321 and expect failed")
			failedPod := createPausePod(f, pausePodConfig{
				Name:      "failed-allocate-port-54321",
				Namespace: f.Namespace.Name,
				Labels: map[string]string{
					targetPodLabel: "true",
				},
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1"),
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "port-54321",
						ContainerPort: 1111,
						HostPort:      54321,
						Protocol:      corev1.ProtocolTCP,
					},
				},
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})

			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, failedPod.Namespace, failedPod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
			}))

			ginkgo.By("Check pods and reservation status")

			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)

			gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal([]corev1.ObjectReference{
				{
					Namespace: pod.Namespace,
					Name:      pod.Name,
					UID:       pod.UID,
				},
			}), "unexpected reservation owner")
		})

		framework.ConformanceIt("Create Reservation enable AllocateOnce, reserve ports only can be allocated once", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			targetPodLabel := "test-reserve-ports"
			reservation.Spec.AllocateOnce = pointer.Bool(true)
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
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "port-54321",
							ContainerPort: 1111,
							HostPort:      54321,
							Protocol:      corev1.ProtocolTCP,
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create one pod allocate port 54321")
			pod := runPausePod(f, pausePodConfig{
				Name:      "allocate-port-54321",
				Namespace: f.Namespace.Name,
				Labels: map[string]string{
					targetPodLabel: "true",
				},
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1"),
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "port-54321",
						ContainerPort: 1111,
						HostPort:      54321,
						Protocol:      corev1.ProtocolTCP,
					},
				},
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})

			ginkgo.By("Create one pod allocate port 54321 and expect failed")
			failedPod := createPausePod(f, pausePodConfig{
				Name:      "failed-allocate-port-54321",
				Namespace: f.Namespace.Name,
				Labels: map[string]string{
					targetPodLabel: "true",
				},
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1"),
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "port-54321",
						ContainerPort: 1111,
						HostPort:      54321,
						Protocol:      corev1.ProtocolTCP,
					},
				},
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})

			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, failedPod.Namespace, failedPod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
			}))

			ginkgo.By("Check pods and reservation status")

			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)

			gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal([]corev1.ObjectReference{
				{
					Namespace: pod.Namespace,
					Name:      pod.Name,
					UID:       pod.UID,
				},
			}), "unexpected reservation owner")
		})

		framework.ConformanceIt("Create Reservation disables AllocateOnce, reserve ports to pod", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			targetPodLabel := "test-reserve-ports"
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
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "port-54321",
							ContainerPort: 1111,
							HostPort:      54321,
							Protocol:      corev1.ProtocolTCP,
						},
						{
							Name:          "port-54322",
							ContainerPort: 2222,
							HostPort:      54322,
							Protocol:      corev1.ProtocolTCP,
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create one pod allocate port 54321")
			pod := runPausePod(f, pausePodConfig{
				Name:      "allocate-port-54321",
				Namespace: f.Namespace.Name,
				Labels: map[string]string{
					targetPodLabel: "true",
				},
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1"),
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "port-54321",
						ContainerPort: 1111,
						HostPort:      54321,
						Protocol:      corev1.ProtocolTCP,
					},
				},
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})

			ginkgo.By("Create one pod allocate port 54321 and 54322")
			runPausePod(f, pausePodConfig{
				Name:      "other-allocate-port-54321-54322",
				Namespace: f.Namespace.Name,
				Labels: map[string]string{
					targetPodLabel: "true",
				},
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1"),
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "port-54321",
						ContainerPort: 1111,
						HostPort:      54321,
						Protocol:      corev1.ProtocolTCP,
					},
					{
						Name:          "port-54322",
						ContainerPort: 2222,
						HostPort:      54322,
						Protocol:      corev1.ProtocolTCP,
					},
				},
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})

			ginkgo.By("Check pods and reservation status")

			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)

			gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal([]corev1.ObjectReference{
				{
					Namespace: pod.Namespace,
					Name:      pod.Name,
					UID:       pod.UID,
				},
			}), "unexpected reservation owner")
		})
	})
})
