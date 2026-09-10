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
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

const (
	// numaHintTargetNodeLabel pins the case to a dedicated node when the environment provides one. The case counts
	// the GPUs of the node to decide how many background pods are needed, so an unrelated GPU workload landing on
	// the same node would silently invalidate the setup.
	numaHintTargetNodeLabel = "e2e-numa-hint-target"

	numaHintOwnerLabel  = "e2e-numa-hint-owner"
	numaHintFillerLabel = "e2e-numa-hint-filler"

	// wholeGPU is the koordinator.sh/gpu request of an entire GPU card.
	wholeGPU = "100"

	// gpusPerReservation is how many cards each reservation of the case holds. Two cards are the minimum letting a
	// reservation span two NUMA nodes, which is what the case needs to tell the hint scopes apart.
	gpusPerReservation = 2
)

var _ = SIGDescribe("NUMAHintReservation", func() {
	f := framework.NewDefaultFramework("numahint-reservation")
	var koordSchedulerName string

	ginkgo.BeforeEach(func() {
		koordSchedulerName = framework.TestContext.KoordSchedulerName
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

	framework.KoordinatorDescribe("BestEffort NUMA hint scoped by the nominated reservation", func() {
		// The invariant under test is that the NUMA affinity the topology manager admits must be one the reservation
		// nominated for the pod can actually satisfy. The hint is instead resolved over every reservation the pod
		// could match, so an affinity backed by a reservation the pod was not nominated to can win the admission and
		// is then rejected while reserving the devices, leaving the pod unschedulable for good.
		//
		// Telling the two scopes apart takes two reservations. A hint scoped to the nomination only ever proposes
		// affinities the nominated reservation can satisfy, so the unnominated reservation has to be the one making a
		// second affinity look feasible. It also has to be the more attractive one, otherwise the wider hint would win
		// anyway and the case would pass on both a correct and a broken scope: the nominated reservation therefore
		// spans two NUMA nodes, so the only affinity satisfying it is the wider, non preferred one, while the
		// unnominated one sits on a single NUMA node and contributes a narrower, preferred affinity. A preferred hint
		// beats a non preferred one whatever the scores are, which is what makes the outcome deterministic instead of
		// depending on how the hint scores happen to be computed.
		framework.ConformanceIt("admits a NUMA affinity the nominated reservation can satisfy", func() {
			node, topology := skipUnlessIdleGPUNodeWithMultiNUMA(f, 2)
			singleNUMA, crossNUMA := skipUnlessGPULayoutFitsCrossNUMAReservation(topology)
			// Every pod below has to reach the node through the scheduler, otherwise no device would be allocated and
			// the GPUs would stay allocatable from the scheduler point of view. So the node is pinned by its hostname
			// label rather than by spec.nodeName, and its taints are tolerated.
			nodeSelector := map[string]string{corev1.LabelHostname: node.Labels[corev1.LabelHostname]}
			tolerations := tolerationsForNode(node)

			ginkgo.By(fmt.Sprintf("Occupy every GPU of node %s with a filler pod", node.Name))
			fillers := occupyEveryGPU(f, koordSchedulerName, topology, nodeSelector, tolerations)

			// Releasing exactly the cards a reservation has to hold is what makes its placement deterministic: no
			// other card of the node is allocatable when it gets scheduled, so the case does not rely on how the
			// device plugin ranks the NUMA nodes and the cards within them.
			ginkgo.By(fmt.Sprintf("Reserve GPUs %v, which sit on the single NUMA node %d", singleNUMA, topology.minorToNUMA[singleNUMA[0]]))
			fillers.release(f, singleNUMA)
			singleNUMAReservation := createGPUReservation(f, koordSchedulerName, "numa-hint-reservation-single-numa", 0, nodeSelector, tolerations)
			gomega.Expect(gpuMinorsOfAllocations(singleNUMAReservation.Annotations)).Should(gomega.ConsistOf(singleNUMA),
				"the single NUMA node reservation did not take the only allocatable GPUs")

			// The reservation order label pins which reservation gets nominated: the nominator picks the matching
			// reservation with the lowest non zero order before it scores the others, so the nomination no longer
			// depends on the reservation scores.
			ginkgo.By(fmt.Sprintf("Reserve GPUs %v, which span two NUMA nodes, and make it the nominated reservation", crossNUMA))
			fillers.release(f, crossNUMA)
			crossNUMAReservation := createGPUReservation(f, koordSchedulerName, "numa-hint-reservation-cross-numa", 1, nodeSelector, tolerations)
			gomega.Expect(gpuMinorsOfAllocations(crossNUMAReservation.Annotations)).Should(gomega.ConsistOf(crossNUMA),
				"the cross NUMA node reservation did not take the only allocatable GPUs")
			gomega.Expect(topology.numaNodesOfGPUAllocations(crossNUMAReservation.Annotations).Len()).Should(gomega.Equal(2),
				"the cross NUMA node reservation is expected to span two NUMA nodes")

			ginkgo.By("Create the target pod matching both reservations with the BestEffort NUMA topology policy")
			pod := createPausePod(f, pausePodConfig{
				Name:      "numa-hint-target",
				Namespace: f.Namespace.Name,
				Labels: map[string]string{
					numaHintOwnerLabel: "true",
				},
				Annotations: map[string]string{
					// BestEffort hints are the ones resolved during Reserve, i.e. once a reservation has been
					// nominated, which is the only phase where the two scopes can differ. The pod deliberately
					// carries no reservation affinity: restricting it to the nominated reservation would narrow the
					// hint scope as a side effect and hide the very behaviour under test.
					apiext.AnnotationNUMATopologySpec: `{"numaTopologyPolicy":"BestEffort"}`,
				},
				Resources:     &corev1.ResourceRequirements{Requests: reservedRequests(), Limits: reservedRequests()},
				SchedulerName: koordSchedulerName,
				NodeSelector:  nodeSelector,
				Tolerations:   tolerations,
			})

			ginkgo.By("Wait for the target pod scheduled")
			framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, pod.Namespace, pod.Name,
				"scheduled on the NUMA nodes of the nominated reservation", 2*time.Minute, func(p *corev1.Pod) (bool, error) {
					_, scheduledCondition := k8spodutil.GetPodCondition(&p.Status, corev1.PodScheduled)
					if scheduledCondition == nil {
						return false, nil
					}
					// The rejection happens while reserving the devices of the nominated reservation, after the
					// NUMA affinity has been admitted, so it is reported as a plain unschedulable pod and is
					// indistinguishable from a pod still waiting for room. Hence the wait for the timeout rather
					// than a fail fast, with the message logged to tell the two apart when it expires.
					if scheduledCondition.Status != corev1.ConditionTrue {
						framework.Logf("The target pod is not scheduled yet: %s: %s", scheduledCondition.Reason, scheduledCondition.Message)
						return false, nil
					}
					return true, nil
				}))

			ginkgo.By("Check the target pod took the GPUs of the nominated reservation")
			expectPodBoundReservation(f.ClientSet, f.KoordinatorClientSet, pod.Namespace, pod.Name, crossNUMAReservation.Name)
			pod, err := f.PodClient().Get(context.TODO(), pod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get the target pod")
			gomega.Expect(gpuMinorsOfAllocations(pod.Annotations)).Should(gomega.ConsistOf(crossNUMA),
				"the pod did not take the GPUs reserved by the nominated reservation")
		})
	})
})

// reservedRequests is what both the reservations and the target pod ask for, so that the pod fits either reservation as
// far as the resources are concerned and only the NUMA topology decides.
func reservedRequests() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("2"),
		corev1.ResourceMemory: resource.MustParse("4Gi"),
		apiext.ResourceGPU:    resource.MustParse(strconv.Itoa(gpusPerReservation * 100)),
	}
}

func fillerRequests() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("1"),
		corev1.ResourceMemory: resource.MustParse("1Gi"),
		apiext.ResourceGPU:    resource.MustParse(wholeGPU),
	}
}

// createGPUReservation creates a Restricted reservation holding gpusPerReservation whole GPUs on the given node and
// waits for it to become available. A non zero order is published as the reservation order label, which the nominator
// honors before scoring.
func createGPUReservation(f *framework.Framework, koordSchedulerName, name string, order int, nodeSelector map[string]string, tolerations []corev1.Toleration) *schedulingv1alpha1.Reservation {
	reservation, err := manifest.ReservationFromManifest("scheduling/simple-reservation.yaml")
	framework.ExpectNoError(err, "unable to load reservation")
	reservation.Name = name
	if order != 0 {
		reservation.Labels[apiext.LabelReservationOrder] = strconv.Itoa(order)
	}
	reservation.Spec.AllocateOnce = ptr.To[bool](false)
	reservation.Spec.AllocatePolicy = schedulingv1alpha1.ReservationAllocatePolicyRestricted
	reservation.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
		{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					numaHintOwnerLabel: "true",
				},
			},
		},
	}
	reservation.Spec.Template.Spec.NodeSelector = nodeSelector
	reservation.Spec.Template.Spec.Tolerations = tolerations
	// The manifest hardcodes koord-scheduler, while the scheduler under test may be deployed under another name.
	reservation.Spec.Template.Spec.SchedulerName = koordSchedulerName
	reservation.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:      "main",
			Resources: corev1.ResourceRequirements{Requests: reservedRequests(), Limits: reservedRequests()},
		},
	}
	reservation, err = f.KoordinatorClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
	framework.ExpectNoError(err, "unable to create reservation %s", name)
	return waitingForReservationScheduled(f.KoordinatorClientSet, reservation)
}

// gpuFillers tracks which filler pod holds which GPU minor, so that a given card can be freed on demand.
type gpuFillers struct {
	podNameOfMinor map[int32]string
}

// release deletes the filler pods holding the given minors and waits for them to be gone, which is what hands the cards
// back to the scheduler.
func (o *gpuFillers) release(f *framework.Framework, minors []int32) {
	var podNames []string
	for _, minor := range minors {
		podName, ok := o.podNameOfMinor[minor]
		gomega.Expect(ok).Should(gomega.BeTrue(), "GPU minor %d is held by no filler pod", minor)
		err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), podName, *metav1.NewDeleteOptions(0))
		framework.ExpectNoError(err, "unable to delete the filler pod %s holding GPU minor %d", podName, minor)
		delete(o.podNameOfMinor, minor)
		podNames = append(podNames, podName)
	}
	// A card is handed back to the scheduler only once the pod holding it is gone, so the reservation created next
	// would otherwise race with the deletion and find nothing allocatable.
	for _, podName := range podNames {
		framework.ExpectNoError(e2epod.WaitForPodNotFoundInNamespace(f.ClientSet, podName, f.Namespace.Name, framework.PollShortTimeout),
			"the filler pod %s is expected to be deleted", podName)
	}
}

// occupyEveryGPU runs one filler pod per GPU of the node, so that from there on the allocatable cards are exactly the
// ones the case deliberately frees.
func occupyEveryGPU(f *framework.Framework, koordSchedulerName string, topology *gpuNUMATopology, nodeSelector map[string]string, tolerations []corev1.Toleration) *gpuFillers {
	var podNames []string
	for i := 0; i < len(topology.minorToNUMA); i++ {
		pod := createPausePod(f, pausePodConfig{
			Name:      fmt.Sprintf("numa-hint-filler-%d", i),
			Namespace: f.Namespace.Name,
			Labels: map[string]string{
				numaHintFillerLabel: "true",
			},
			Resources:     &corev1.ResourceRequirements{Requests: fillerRequests(), Limits: fillerRequests()},
			SchedulerName: koordSchedulerName,
			NodeSelector:  nodeSelector,
			Tolerations:   tolerations,
		})
		podNames = append(podNames, pod.Name)
	}

	fillers := &gpuFillers{podNameOfMinor: map[int32]string{}}
	for _, podName := range podNames {
		framework.ExpectNoError(e2epod.WaitForPodCondition(f.ClientSet, f.Namespace.Name, podName,
			"scheduled", framework.PollShortTimeout, func(p *corev1.Pod) (bool, error) {
				return p.Spec.NodeName != "", nil
			}), "the filler pods are expected to fit the GPUs of node %s", topology.nodeName)
		pod, err := f.PodClient().Get(context.TODO(), podName, metav1.GetOptions{})
		framework.ExpectNoError(err, "unable to get the filler pod %s", podName)
		minors := gpuMinorsOfAllocations(pod.Annotations)
		gomega.Expect(minors).Should(gomega.HaveLen(1), "the filler pod %s is expected to hold a single GPU", podName)
		gomega.Expect(fillers.podNameOfMinor).ShouldNot(gomega.HaveKey(minors[0]), "GPU minor %d is held by two filler pods", minors[0])
		fillers.podNameOfMinor[minors[0]] = podName
	}
	gomega.Expect(fillers.podNameOfMinor).Should(gomega.HaveLen(len(topology.minorToNUMA)), "the filler pods did not take every GPU of node %s", topology.nodeName)
	return fillers
}

// gpuNUMATopology is the GPU layout of a single node, read from its Device object.
type gpuNUMATopology struct {
	nodeName     string
	minorToNUMA  map[int32]int32
	numaToMinors map[int32][]int32
}

// gpuMinorsOfAllocations returns the GPU minors the device allocations of the annotations hold.
func gpuMinorsOfAllocations(annotations map[string]string) []int32 {
	allocations, err := apiext.GetDeviceAllocations(annotations)
	framework.ExpectNoError(err, "unable to get device allocations")
	var minors []int32
	for _, allocation := range allocations[schedulingv1alpha1.GPU] {
		minors = append(minors, allocation.Minor)
	}
	return minors
}

func (t *gpuNUMATopology) numaNodesOfGPUAllocations(annotations map[string]string) sets.Set[int32] {
	allocations, err := apiext.GetDeviceAllocations(annotations)
	framework.ExpectNoError(err, "unable to get device allocations")
	numaNodes := sets.New[int32]()
	for _, allocation := range allocations[schedulingv1alpha1.GPU] {
		numaNode, ok := t.minorToNUMA[allocation.Minor]
		gomega.Expect(ok).Should(gomega.BeTrue(), "GPU minor %d is missing from the device topology of node %s", allocation.Minor, t.nodeName)
		numaNodes.Insert(numaNode)
	}
	gomega.Expect(numaNodes.Len()).ShouldNot(gomega.BeZero(), "no GPU allocation found in annotations of node %s", t.nodeName)
	return numaNodes
}

func newGPUNUMATopology(device *schedulingv1alpha1.Device) *gpuNUMATopology {
	topology := &gpuNUMATopology{
		nodeName:     device.Name,
		minorToNUMA:  map[int32]int32{},
		numaToMinors: map[int32][]int32{},
	}
	for i := range device.Spec.Devices {
		info := &device.Spec.Devices[i]
		if info.Type != schedulingv1alpha1.GPU || !info.Health || info.Minor == nil || info.Topology == nil {
			continue
		}
		topology.minorToNUMA[*info.Minor] = info.Topology.NodeID
		topology.numaToMinors[info.Topology.NodeID] = append(topology.numaToMinors[info.Topology.NodeID], *info.Minor)
	}
	// The Device object lists the GPUs in an unspecified order, while the cases pick the cards to free by index and
	// report them, both of which are easier to follow when the minors are sorted.
	for _, minors := range topology.numaToMinors {
		sort.Slice(minors, func(i, j int) bool { return minors[i] < minors[j] })
	}
	return topology
}

// skipUnlessGPULayoutFitsCrossNUMAReservation picks the GPUs of the two reservations the case needs: gpusPerReservation
// cards on a single NUMA node, plus gpusPerReservation cards spread over that NUMA node and another one. It returns the
// two sets of minors and skips the case when the GPU layout of the node cannot host them.
func skipUnlessGPULayoutFitsCrossNUMAReservation(topology *gpuNUMATopology) (singleNUMA, crossNUMA []int32) {
	// The NUMA node hosting the single NUMA node reservation also contributes all but one of the cards of the cross
	// NUMA node one, so that a second NUMA node with a single card is enough.
	needed := gpusPerReservation + gpusPerReservation - 1
	numaNodes := make([]int32, 0, len(topology.numaToMinors))
	for numaNode := range topology.numaToMinors {
		numaNodes = append(numaNodes, numaNode)
	}
	sort.Slice(numaNodes, func(i, j int) bool { return numaNodes[i] < numaNodes[j] })

	for _, crowded := range numaNodes {
		if len(topology.numaToMinors[crowded]) < needed {
			continue
		}
		for _, other := range numaNodes {
			if other == crowded || len(topology.numaToMinors[other]) < 1 {
				continue
			}
			singleNUMA = topology.numaToMinors[crowded][:gpusPerReservation]
			crossNUMA = append(crossNUMA, topology.numaToMinors[crowded][gpusPerReservation:needed]...)
			crossNUMA = append(crossNUMA, topology.numaToMinors[other][0])
			return singleNUMA, crossNUMA
		}
	}
	ginkgo.Skip(fmt.Sprintf("node %s exposes no NUMA node with %d healthy GPUs next to a NUMA node with one", topology.nodeName, needed))
	return nil, nil
}

// skipUnlessIdleGPUNodeWithMultiNUMA looks for a schedulable node exposing healthy GPUs on at least minNUMANodes NUMA
// nodes and currently running no other GPU pod. Nodes labelled with numaHintTargetNodeLabel are considered first so
// that an environment can pin the case to a node it has drained. The case is skipped rather than failed when nothing
// matches, because the CI clusters carry no GPU at all.
func skipUnlessIdleGPUNodeWithMultiNUMA(f *framework.Framework, minNUMANodes int) (*corev1.Node, *gpuNUMATopology) {
	deviceList, err := f.KoordinatorClientSet.SchedulingV1alpha1().Devices().List(context.TODO(), metav1.ListOptions{})
	framework.ExpectNoError(err, "unable to list Device")

	var fallbackNode *corev1.Node
	var fallbackTopology *gpuNUMATopology
	for i := range deviceList.Items {
		topology := newGPUNUMATopology(&deviceList.Items[i])
		if len(topology.numaToMinors) < minNUMANodes {
			continue
		}
		node, err := f.ClientSet.CoreV1().Nodes().Get(context.TODO(), topology.nodeName, metav1.GetOptions{})
		if err != nil || node.Spec.Unschedulable {
			continue
		}
		// The hostname label is not necessarily the node name, and it is the only handle the pods have to land here.
		if node.Labels[corev1.LabelHostname] == "" {
			framework.Logf("Skipping node %s for the NUMA hint case, it carries no %s label", node.Name, corev1.LabelHostname)
			continue
		}
		if occupied := gpuPodsOnNode(f, node.Name); occupied > 0 {
			framework.Logf("Skipping node %s for the NUMA hint case, %d GPU pods are still running on it", node.Name, occupied)
			continue
		}
		if node.Labels[numaHintTargetNodeLabel] == "true" {
			return node, topology
		}
		if fallbackNode == nil {
			fallbackNode, fallbackTopology = node, topology
		}
	}
	if fallbackNode != nil {
		return fallbackNode, fallbackTopology
	}
	ginkgo.Skip(fmt.Sprintf("no idle schedulable node exposes healthy GPUs on at least %d NUMA nodes", minNUMANodes))
	return nil, nil
}

func gpuPodsOnNode(f *framework.Framework, nodeName string) int {
	podList, err := f.ClientSet.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	framework.ExpectNoError(err, "unable to list pods of node %s", nodeName)
	count := 0
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if requestsGPU(pod) {
			count++
		}
	}
	return count
}

// requestsGPU reports whether any container asks for a GPU flavored resource. The device plugins expose several names,
// e.g. koordinator.sh/gpu, koordinator.sh/gpu-core and nvidia.com/gpu, and matching only one of them would let a GPU
// workload pass unnoticed.
func requestsGPU(pod *corev1.Pod) bool {
	containers := append([]corev1.Container{}, pod.Spec.Containers...)
	containers = append(containers, pod.Spec.InitContainers...)
	for i := range containers {
		for name := range containers[i].Resources.Requests {
			if strings.Contains(strings.ToLower(string(name)), "gpu") {
				return true
			}
		}
	}
	return false
}

// tolerationsForNode tolerates every taint of the node, so that a GPU node carrying resource pool taints can host the
// case without the taints being hardcoded here.
func tolerationsForNode(node *corev1.Node) []corev1.Toleration {
	var tolerations []corev1.Toleration
	for _, taint := range node.Spec.Taints {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      taint.Key,
			Operator: corev1.TolerationOpExists,
			Effect:   taint.Effect,
		})
	}
	return tolerations
}
