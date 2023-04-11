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
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	ginkgo.BeforeEach(func() {

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
				SchedulerName:     "koord-scheduler",
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
				SchedulerName:     "koord-scheduler",
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
			reservation.Spec.AllocateOnce = false
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
})
