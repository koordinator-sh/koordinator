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
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("Reservation", func() {
	f := framework.NewDefaultFramework("reservation")
	var nodeList *corev1.NodeList

	ginkgo.BeforeEach(func() {
		nodeList = &corev1.NodeList{}
		var err error

		framework.AllNodesReady(f.ClientSet, time.Minute)

		nodeList, err = e2enode.GetReadySchedulableNodes(f.ClientSet)
		if err != nil {
			framework.Logf("Unexpected error occurred: %v", err)
		}
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

	framework.KoordinatorDescribe("Basic Reservation functionality", func() {
		framework.ConformanceIt("Create Reservation enables AllocateOnce and reserves CPU and Memory for Pod", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod to consume reservation")
			pod, err := manifest.PodFromManifest("scheduling/simple-pod-with-reservation.yaml")
			framework.ExpectNoError(err, "unable to load pod")
			pod.Namespace = f.Namespace.Name

			pod = f.PodClient().Create(pod)
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod), "unable to schedule pod")

			ginkgo.By("Check pod and reservation status")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, pod.Namespace, pod.Name, reservation.Name)

			gomega.Eventually(func() bool {
				r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				return r.Status.Phase == schedulingv1alpha1.ReservationSucceeded
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))

			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			reservationRequests := reservationutil.ReservationRequests(reservation)
			gomega.Expect(reservation.Status.Allocatable).Should(gomega.Equal(reservationRequests))

			podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
			podRequests = quotav1.Mask(podRequests, quotav1.ResourceNames(reservation.Status.Allocatable))
			gomega.Expect(reservation.Status.Allocated).Should(gomega.Equal(podRequests))
			gomega.Expect(reservation.Status.CurrentOwners).Should(gomega.Equal([]corev1.ObjectReference{
				{
					Namespace: pod.Namespace,
					Name:      pod.Name,
					UID:       pod.UID,
				},
			}), "reservation.status.currentOwners is not as expected")
		})

		framework.ConformanceIt("Create Reservation disables AllocateOnce and reserves CPU and Memory for tow Pods", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			// disable allocateOnce
			reservation.Spec.AllocateOnce = pointer.Bool(false)
			// reserve resources for two Pods
			for k, v := range reservation.Spec.Template.Spec.Containers[0].Resources.Requests {
				vv := v.DeepCopy()
				vv.Add(v)
				reservation.Spec.Template.Spec.Containers[0].Resources.Requests[k] = vv
				reservation.Spec.Template.Spec.Containers[0].Resources.Limits[k] = vv
			}

			reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Loading pod from manifest")
			pod, err := manifest.PodFromManifest("scheduling/simple-pod-with-reservation.yaml")
			framework.ExpectNoError(err)
			pod.Namespace = f.Namespace.Name

			ginkgo.By("Create Pod to consume Reservation")
			for i := 0; i < 2; i++ {
				testPod := pod.DeepCopy()
				testPod.Name = fmt.Sprintf("%s-%d", pod.Name, i)
				f.PodClient().Create(testPod)
				framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, testPod), "unable to schedule pod")
			}

			ginkgo.By("Check pod and reservation Status")

			r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get reservation")

			var owners []corev1.ObjectReference
			for i := 0; i < 2; i++ {
				name := fmt.Sprintf("%s-%d", pod.Name, i)
				p, err := f.PodClient().Get(context.TODO(), name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				gomega.Expect(p.Spec.NodeName).Should(gomega.Equal(r.Status.NodeName),
					fmt.Sprintf("reservation is scheduled to node %v but pod is scheduled to node %v", r.Status.NodeName, p.Spec.NodeName))

				reservationAllocated, err := apiext.GetReservationAllocated(p)
				framework.ExpectNoError(err)
				gomega.Expect(reservationAllocated).Should(gomega.Equal(&apiext.ReservationAllocated{
					Name: r.Name,
					UID:  r.UID,
				}))

				owners = append(owners, corev1.ObjectReference{
					Namespace: p.Namespace,
					Name:      p.Name,
					UID:       p.UID,
				})
			}

			gomega.Eventually(func() bool {
				r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				return len(r.Status.CurrentOwners) == 2
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))

			r, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			reservationRequests := reservationutil.ReservationRequests(r)
			gomega.Expect(r.Status.Allocatable).Should(gomega.Equal(reservationRequests))

			podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
			podRequests = quotav1.Mask(podRequests, quotav1.ResourceNames(r.Status.Allocatable))
			for k, v := range podRequests {
				vv := v.DeepCopy()
				vv.Add(v)
				podRequests[k] = vv
			}
			gomega.Expect(equality.Semantic.DeepEqual(r.Status.Allocated, podRequests)).Should(gomega.Equal(true))
			sort.Slice(owners, func(i, j int) bool {
				return owners[i].UID < owners[j].UID
			})
			sort.Slice(r.Status.CurrentOwners, func(i, j int) bool {
				return r.Status.CurrentOwners[i].UID < r.Status.CurrentOwners[j].UID
			})
			gomega.Expect(r.Status.CurrentOwners).Should(gomega.Equal(owners))
		})

		ginkgo.Context("validates resource fit with reservations", func() {
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

			framework.ConformanceIt("reserve all remaining resources to prevent other pods from being scheduled", func() {
				ginkgo.By("Create Reservation")
				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable to load reservation")

				reservation.Spec.Template.Spec.NodeName = testNodeName
				reservation.Spec.Template.Spec.Containers = append(reservation.Spec.Template.Spec.Containers, corev1.Container{
					Name: "fake-resource-container",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
						Requests: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
					},
				})
				_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				framework.ExpectNoError(err, "unable to create reservation: %v", reservation.Name)

				ginkgo.By("Wait for Reservation Scheduled")
				waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

				ginkgo.By("Create Pod")
				pod := createPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
						Requests: corev1.ResourceList{
							fakeResourceName: resource.MustParse("1000"),
						},
					},
					NodeName:      testNodeName,
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				})

				ginkgo.By("Wait for Pod schedule failed")
				framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
					_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
					return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
				}))
			})

			framework.ConformanceIt("after a Pod uses a Reservation, the remaining resources of the node can be reserved by another reservation", func() {
				ginkgo.By("Create reservation")
				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable to load reservation")

				reservation.Spec.AllocateOnce = pointer.Bool(false)
				reservation.Spec.Template.Spec.NodeName = testNodeName
				reservation.Spec.Template.Spec.Containers = append(reservation.Spec.Template.Spec.Containers, corev1.Container{
					Name: "fake-resource-container",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							fakeResourceName: resource.MustParse("500"),
						},
						Requests: corev1.ResourceList{
							fakeResourceName: resource.MustParse("500"),
						},
					},
				})
				reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"e2e-reserve-resource": "true",
							},
						},
					},
				}
				_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				framework.ExpectNoError(err, "unable to create reservation: %v", reservation.Name)

				ginkgo.By("Wait for reservation scheduled")
				waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

				ginkgo.By("Create pod and wait for scheduled")
				pod := createPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Labels: map[string]string{
						"e2e-reserve-resource": "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							fakeResourceName: resource.MustParse("500"),
						},
						Requests: corev1.ResourceList{
							fakeResourceName: resource.MustParse("500"),
						},
					},
					NodeName:      testNodeName,
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				})
				framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod))

				pod, err = f.PodClient().Get(context.TODO(), pod.Name, metav1.GetOptions{})
				gomega.Expect(err).NotTo(gomega.HaveOccurred())

				ginkgo.By("Create other reservation reserves the remaining fakeResource")
				otherReservation := reservation.DeepCopy()
				otherReservation.Name = "e2e-other-reservation"
				otherReservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"e2e-reserve-resource": "false",
							},
						},
					},
				}
				_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), otherReservation, metav1.CreateOptions{})
				framework.ExpectNoError(err, "unable to create reservation: %v", otherReservation.Name)

				ginkgo.By("Wait for Reservation Scheduled")
				waitingForReservationScheduled(f.KoordinatorClientSet, otherReservation)

				r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), otherReservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				gomega.Expect(r.Status.NodeName).Should(gomega.Equal(pod.Spec.NodeName))
			})
		})

		framework.ConformanceIt("select reservation via reservation affinity", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")
			if reservation.Labels == nil {
				reservation.Labels = map[string]string{}
			}
			reservation.Labels["e2e-select-reservation"] = "true"
			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod that must be failed to schedule")

			reservationAffinity := &apiext.ReservationAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &apiext.ReservationAffinitySelector{
					ReservationSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "e2e-select-reservation",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"false"},
								},
							},
						},
					},
				},
			}
			affinityData, err := json.Marshal(reservationAffinity)
			framework.ExpectNoError(err, "failed to marshal reservation")

			pod := createPausePod(f, pausePodConfig{
				Name: string(uuid.NewUUID()),
				Labels: map[string]string{
					"app": "e2e-test-reservation",
				},
				Annotations: map[string]string{
					apiext.AnnotationReservationAffinity: string(affinityData),
				},
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
			}))

			ginkgo.By("Create pod and wait for scheduled")
			reservationAffinity = &apiext.ReservationAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &apiext.ReservationAffinitySelector{
					ReservationSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "e2e-select-reservation",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
					},
				},
			}
			affinityData, err = json.Marshal(reservationAffinity)
			framework.ExpectNoError(err, "failed to marshal reservation")
			pod = createPausePod(f, pausePodConfig{
				Name: string(uuid.NewUUID()),
				Labels: map[string]string{
					"app": "e2e-test-reservation",
				},
				Annotations: map[string]string{
					apiext.AnnotationReservationAffinity: string(affinityData),
				},
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod))
		})

		framework.ConformanceIt(
			"Create reservation with Restricted policy, reserves 4Core8Gi, "+
				"and create 4 Pods try to allocate from reservation, but 2 Pods can allocate 2Core2Gi "+
				"1 Pod can allocate 3Gi, 1 Pod failed with 2Gi",
			func() {
				ginkgo.By("Create reservation")
				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable to load reservation")

				targetPodLabel := "test-reserve-policy"
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
								corev1.ResourceCPU:    resource.MustParse("4"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
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
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
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

				ginkgo.By("Create the third Pod also matched the reservation and allocate 3Gi")
				runPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("3Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("3Gi"),
						},
					},
					NodeName:      reservation.Status.NodeName,
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				})

				ginkgo.By("Create the fourth Pod also matched the reservation and allocate 2Gi but failed")
				pod := createPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
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
				gomega.Expect(reservedCount).Should(gomega.Equal(3), "no pods using the expected reservation")

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
			"Create reservation with Aligned policy, reserves 4Core8Gi, "+
				"and create 4 Pods try to allocate from reservation, but 2 Pods allocate 2Core2Gi from reservation "+
				"1 Pod allocates 3Gi from reservation, 1 Pod allocates 2Gi from reservation and node",
			func() {
				ginkgo.By("Create reservation")
				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable to load reservation")

				targetPodLabel := "test-reserve-policy"
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
								corev1.ResourceCPU:    resource.MustParse("4"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
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
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
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

				ginkgo.By("Create the third Pod also matched the reservation and allocate 3Gi")
				runPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("3Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("3Gi"),
						},
					},
					NodeName:      reservation.Status.NodeName,
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				})

				ginkgo.By("Create the fourth Pod also matched the reservation and allocate 2Gi but failed")
				runPausePod(f, pausePodConfig{
					Name: string(uuid.NewUUID()),
					Labels: map[string]string{
						targetPodLabel: "true",
					},
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					NodeName:      reservation.Status.NodeName,
					SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
				})

				podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				framework.ExpectNoError(err)

				ginkgo.By("Check pods and reservation status, must has 4 pods allocate from reservation")
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
				gomega.Expect(reservedCount).Should(gomega.Equal(4), "no pods using the expected reservation")

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

		ginkgo.Context("validates resource fit with Aligned Reservations", func() {
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

			framework.ConformanceIt(
				"Create a batch of small-sized Reservations that use the Aligned policy to fill up the machine. "+
					"Pods that match the Reservation, but whose size is larger than the Reservation, expect allocation to fail.",
				func() {
					ginkgo.By("Create reservation")
					reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
					framework.ExpectNoError(err, "unable to load reservation")

					targetPodLabel := "test-reserve-policy"
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
									fakeResourceName: resource.MustParse("200"),
								},
							},
						},
					}
					for i := 0; i < 5; i++ {
						reservation.Name = fmt.Sprintf("reservation-%d", i)
						_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
						framework.ExpectNoError(err, "unable to create reservation")

						ginkgo.By("Wait for reservation scheduled")
						waitingForReservationScheduled(f.KoordinatorClientSet, reservation)
					}

					ginkgo.By("Create 1 Pod matched the reservation but failed scheduling")
					pod := createPausePod(f, pausePodConfig{
						Name: string(uuid.NewUUID()),
						Labels: map[string]string{
							targetPodLabel: "true",
						},
						Resources: &corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								fakeResourceName: resource.MustParse("300"),
							},
							Requests: corev1.ResourceList{
								fakeResourceName: resource.MustParse("300"),
							},
						},
						NodeName:      reservation.Status.NodeName,
						SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
					})
					ginkgo.By("Wait for Pod schedule failed")
					framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
						_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
						return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
					}))
				},
			)
		})

		framework.ConformanceIt("validates PodAntiAffinity with reservation", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			reservation.Spec.Template.Namespace = f.Namespace.Name
			reservation.Spec.Template.Labels = map[string]string{
				"e2e-reservation-interpodaffinity": "true",
			}

			reservation.Spec.Template.Spec.Affinity = &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"e2e-reservation-interpodaffinity": "true",
								},
							},
							TopologyKey: corev1.LabelHostname,
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod and wait for scheduled")
			pod := createPausePod(f, pausePodConfig{
				Name: string(uuid.NewUUID()),
				Labels: map[string]string{
					"e2e-reservation-interpodaffinity": "true",
					"app":                              "e2e-test-reservation",
				},
				Affinity:      reservation.Spec.Template.Spec.Affinity,
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod))

			p, err := f.PodClient().Get(context.TODO(), pod.Name, metav1.GetOptions{})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(p.Spec.NodeName).Should(gomega.Equal(reservation.Status.NodeName))
		})

		framework.ConformanceIt("validates PodAntiAffinity with reservation - Pod failed to schedule", func() {
			ginkgo.By("Create reservation")
			reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
			framework.ExpectNoError(err, "unable to load reservation")

			reservation.Spec.Template.Namespace = f.Namespace.Name
			reservation.Spec.Template.Labels = map[string]string{
				"e2e-reservation-interpodaffinity": "true",
			}

			reservation.Spec.Template.Spec.Affinity = &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"e2e-reservation-interpodaffinity": "true",
								},
							},
							TopologyKey: corev1.LabelHostname,
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
			framework.ExpectNoError(err, "unable to create reservation")

			ginkgo.By("Wait for reservation scheduled")
			reservation = waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

			ginkgo.By("Create pod and wait for scheduled")
			pod := createPausePod(f, pausePodConfig{
				Name: string(uuid.NewUUID()),
				Labels: map[string]string{
					"e2e-reservation-interpodaffinity": "true",
				},
				Affinity:      reservation.Spec.Template.Spec.Affinity,
				NodeName:      reservation.Status.NodeName,
				SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
			})
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name, "wait for pod schedule failed", 60*time.Second, func(pod *corev1.Pod) (bool, error) {
				_, scheduledCondition := k8spodutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
				return scheduledCondition != nil && scheduledCondition.Status == corev1.ConditionFalse, nil
			}))
		})

		ginkgo.Context("PodTopologySpread Filtering With Reservation", func() {
			var nodeNames []string
			topologyKey := "kubernetes.io/e2e-pts-filter"

			ginkgo.BeforeEach(func() {
				if len(nodeList.Items) < 2 {
					ginkgo.Skip("At least 2 nodes are required to run the test")
				}
				ginkgo.By("Trying to get 2 available nodes which can run pod")
				nodeNames = Get2NodesThatCanRunPod(f)
				ginkgo.By(fmt.Sprintf("Apply dedicated topologyKey %v for this test on the 2 nodes.", topologyKey))
				for _, nodeName := range nodeNames {
					framework.AddOrUpdateLabelOnNode(f.ClientSet, nodeName, topologyKey, nodeName)
				}
			})
			ginkgo.AfterEach(func() {
				for _, nodeName := range nodeNames {
					framework.RemoveLabelOffNode(f.ClientSet, nodeName, topologyKey)
				}
			})

			ginkgo.It("validates 4 pods with MaxSkew=1 are evenly distributed into 2 nodes", func() {
				ginkgo.By("Create Reservation")
				podLabel := "e2e-pts-filter"

				reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
				framework.ExpectNoError(err, "unable load reservation from manifest")

				reservation.Spec.AllocateOnce = pointer.Bool(false)
				reservation.Spec.Template.Namespace = f.Namespace.Name
				if reservation.Spec.Template.Labels == nil {
					reservation.Spec.Template.Labels = map[string]string{}
				}
				reservation.Spec.Template.Labels[podLabel] = ""
				reservation.Spec.Template.Spec.Affinity = &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      topologyKey,
											Operator: corev1.NodeSelectorOpIn,
											Values:   nodeNames,
										},
									},
								},
							},
						},
					},
				}
				reservation.Spec.Template.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       topologyKey,
						WhenUnsatisfiable: corev1.DoNotSchedule,
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      podLabel,
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
					},
				}

				_, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				framework.ExpectNoError(err, "unable to create reservation: %v", reservation.Name)

				ginkgo.By("Wait for Reservation Scheduled")
				waitingForReservationScheduled(f.KoordinatorClientSet, reservation)

				ginkgo.By("Create 4 pods")
				requests := reservationutil.ReservationRequests(reservation)
				replicas := 4
				rsConfig := pauseRSConfig{
					Replicas: int32(replicas),
					PodConfig: pausePodConfig{
						Name:      podLabel,
						Namespace: f.Namespace.Name,
						Labels: map[string]string{
							podLabel: "",
							"app":    "e2e-test-reservation",
						},
						Resources: &corev1.ResourceRequirements{
							Limits:   requests,
							Requests: requests,
						},
						SchedulerName: reservation.Spec.Template.Spec.SchedulerName,
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      topologyKey,
													Operator: corev1.NodeSelectorOpIn,
													Values:   nodeNames,
												},
											},
										},
									},
								},
							},
						},
						TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
							{
								MaxSkew:           1,
								TopologyKey:       topologyKey,
								WhenUnsatisfiable: corev1.DoNotSchedule,
								LabelSelector: &metav1.LabelSelector{
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      podLabel,
											Operator: metav1.LabelSelectorOpExists,
										},
									},
								},
							},
						},
					},
				}
				runPauseRS(f, rsConfig)
				podList, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				framework.ExpectNoError(err)
				numInNode1, numInNode2 := 0, 0
				for _, pod := range podList.Items {
					if pod.Spec.NodeName == nodeNames[0] {
						numInNode1++
					} else if pod.Spec.NodeName == nodeNames[1] {
						numInNode2++
					}
				}
				expected := replicas / len(nodeNames)
				framework.ExpectEqual(numInNode1, expected, fmt.Sprintf("Pods are not distributed as expected on node %q", nodeNames[0]))
				framework.ExpectEqual(numInNode2, expected, fmt.Sprintf("Pods are not distributed as expected on node %q", nodeNames[1]))

				ginkgo.By("Check Reservation Status")
				gomega.Eventually(func() bool {
					r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
					framework.ExpectNoError(err)
					return len(r.Status.CurrentOwners) == 1
				}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))

				r, err := f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				gomega.Expect(len(r.Status.Allocatable) > 0).Should(gomega.Equal(true))
				gomega.Expect(len(r.Status.CurrentOwners) == 1).Should(gomega.Equal(true))
			})
		})
	})
})
