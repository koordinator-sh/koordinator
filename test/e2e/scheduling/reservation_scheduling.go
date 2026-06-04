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
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("Reservation Scheduling", func() {
	f := framework.NewDefaultFramework("reservation-scheduling")

	ginkgo.BeforeEach(func() {
		framework.AllNodesReady(f.ClientSet, time.Minute)
	})

	ginkgo.AfterEach(func() {
		ls := metav1.SetAsLabelSelector(map[string]string{
			"e2e-test-reservation-scheduling": "true",
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

	framework.KoordinatorDescribe("Reservation TTL expiration", func() {
		framework.ConformanceIt("reservation with short TTL should expire and transition to Failed phase", func() {
			ginkgo.By("Create reservation with short TTL")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			if reservation.Labels == nil {
				reservation.Labels = map[string]string{}
			}
			reservation.Labels["e2e-test-reservation-scheduling"] = "true"
			reservation.Spec.TTL = &metav1.Duration{Duration: 10 * time.Second}
			// Use a label owner that will never match any pod, so the reservation stays unused.
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"e2e-never-match": "true",
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Wait for reservation to expire and transition to Failed")
			gomega.Eventually(func() schedulingv1alpha1.ReservationPhase {
				r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				return r.Status.Phase
			}, 120*time.Second, 2*time.Second).Should(gomega.Equal(schedulingv1alpha1.ReservationFailed))
		})
	})

	framework.KoordinatorDescribe("Reservation with Taints", func() {
		framework.ConformanceIt("reservation with taints can be scheduled and pod with matching toleration can use it", func() {
			ginkgo.By("Create reservation with taints")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			if reservation.Labels == nil {
				reservation.Labels = map[string]string{}
			}
			reservation.Labels["e2e-test-reservation-scheduling"] = "true"
			reservation.Spec.AllocateOnce = ptr.To[bool](true)
			reservation.Spec.Taints = []corev1.Taint{
				{
					Key:    "e2e-reservation-taint",
					Value:  "test-value",
					Effect: corev1.TaintEffectNoSchedule,
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod with matching toleration to consume reservation")
			pod := createPausePod(f, pausePodConfig{
				Name: string(uuid.NewUUID()),
				Labels: map[string]string{
					"app": "e2e-test-reservation",
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "e2e-reservation-taint",
						Operator: corev1.TolerationOpEqual,
						Value:    "test-value",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod), "unable to schedule pod with toleration")

			ginkgo.By("Verify pod is bound to reservation")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, pod.Namespace, pod.Name, reservation.Name)
		})
	})

	framework.KoordinatorDescribe("Reservation resource release after pod deletion", func() {
		framework.ConformanceIt("AllocateOnce reservation transitions to Succeeded after pod is deleted", func() {
			ginkgo.By("Create reservation with AllocateOnce")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			if reservation.Labels == nil {
				reservation.Labels = map[string]string{}
			}
			reservation.Labels["e2e-test-reservation-scheduling"] = "true"
			reservation.Spec.AllocateOnce = ptr.To[bool](true)

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod to consume reservation")
			pod := createPausePod(f, pausePodConfig{
				Name: string(uuid.NewUUID()),
				Labels: map[string]string{
					"app": "e2e-test-reservation",
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod), "unable to schedule pod")

			ginkgo.By("Verify pod is bound to reservation")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, pod.Namespace, pod.Name, reservation.Name)

			ginkgo.By("Verify reservation transitions to Succeeded")
			gomega.Eventually(func() schedulingv1alpha1.ReservationPhase {
				r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				return r.Status.Phase
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(schedulingv1alpha1.ReservationSucceeded))

			ginkgo.By("Delete the pod using the reservation")
			err = f.ClientSet.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err, "unable to delete pod")

			ginkgo.By("Verify reservation remains Succeeded after pod deletion")
			gomega.Consistently(func() schedulingv1alpha1.ReservationPhase {
				r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				return r.Status.Phase
			}, 10*time.Second, 2*time.Second).Should(gomega.Equal(schedulingv1alpha1.ReservationSucceeded))
		})
	})

	framework.KoordinatorDescribe("Reservation with node affinity targeting specific node", func() {
		framework.ConformanceIt("reservation with NodeAffinity is scheduled to the targeted node", func() {
			ginkgo.By("Get a node that can run pod")
			testNodeName := GetNodeThatCanRunPod(f)

			node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), testNodeName, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node")
			hostname := node.Labels[corev1.LabelHostname]

			ginkgo.By("Create reservation with NodeAffinity targeting the specific node")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			if reservation.Labels == nil {
				reservation.Labels = map[string]string{}
			}
			reservation.Labels["e2e-test-reservation-scheduling"] = "true"
			reservation.Spec.AllocateOnce = ptr.To[bool](true)
			reservation.Spec.Template.Spec.Affinity = &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      corev1.LabelHostname,
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{hostname},
									},
								},
							},
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Verify reservation is scheduled to the targeted node")
			gomega.Expect(reservation.Status.NodeName).Should(gomega.Equal(testNodeName),
				fmt.Sprintf("reservation should be scheduled to %s but got %s", testNodeName, reservation.Status.NodeName))

			ginkgo.By("Create pod matching the reservation with same node affinity")
			pod := createPausePod(f, pausePodConfig{
				Name: string(uuid.NewUUID()),
				Labels: map[string]string{
					"app": "e2e-test-reservation",
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      corev1.LabelHostname,
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{hostname},
										},
									},
								},
							},
						},
					},
				},
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod), "unable to schedule pod")

			ginkgo.By("Verify pod is scheduled to the same node as reservation")
			pod, err = f.ClientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			gomega.Expect(pod.Spec.NodeName).Should(gomega.Equal(testNodeName))

			ginkgo.By("Verify pod is bound to reservation")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, pod.Namespace, pod.Name, reservation.Name)
		})
	})

	framework.KoordinatorDescribe("Reservation owner matching by label selector", func() {
		framework.ConformanceIt("pod matching reservation owner label selector can use the reservation", func() {
			ginkgo.By("Create reservation with label selector owner")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			if reservation.Labels == nil {
				reservation.Labels = map[string]string{}
			}
			reservation.Labels["e2e-test-reservation-scheduling"] = "true"
			reservation.Spec.AllocateOnce = ptr.To[bool](true)
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"e2e-owner-selector": "true",
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod with matching owner label")
			pod := createPausePod(f, pausePodConfig{
				Name: string(uuid.NewUUID()),
				Labels: map[string]string{
					"e2e-owner-selector": "true",
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod), "unable to schedule pod")

			ginkgo.By("Verify pod is bound to reservation")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, pod.Namespace, pod.Name, reservation.Name)
		})
	})

	framework.KoordinatorDescribe("Unmatched pod cannot use reservation", func() {
		framework.ConformanceIt("pod not matching reservation owner should not be allocated from reservation", func() {
			ginkgo.By("Create reservation with specific owner labels")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			if reservation.Labels == nil {
				reservation.Labels = map[string]string{}
			}
			reservation.Labels["e2e-test-reservation-scheduling"] = "true"
			reservation.Spec.AllocateOnce = ptr.To[bool](false)
			reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"e2e-matched-owner": "true",
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod that does NOT match reservation owner")
			pod := createPausePod(f, pausePodConfig{
				Name: string(uuid.NewUUID()),
				Labels: map[string]string{
					"e2e-unmatched": "true",
				},
				Resources: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod), "unable to schedule pod")

			ginkgo.By("Verify pod is NOT using the reservation")
			pod, err = f.ClientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			reservationAllocated, err := apiext.GetReservationAllocated(pod)
			framework.ExpectNoError(err)
			gomega.Expect(reservationAllocated).Should(gomega.BeNil(),
				"pod should not be allocated from reservation since it does not match any owner")

			ginkgo.By("Verify reservation has no current owners")
			r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			gomega.Expect(r.Status.CurrentOwners).Should(gomega.BeEmpty(),
				"reservation should have no current owners")
		})
	})
})
