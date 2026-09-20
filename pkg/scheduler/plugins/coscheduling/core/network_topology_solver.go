package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
)

const (
	MessageNoCandidateTopologyNodes = "no candidate topology nodes can accommodate job, desiredOfferSlot: %d, %s, %s"
	// maxDiagnosticTopologyNodeReasons bounds the per-topology-node breakdown emitted by the
	// diagnostic log. A gang that cannot be placed is retried, so an unbounded breakdown
	// (thousands of topology nodes in a large cluster) would be re-emitted on every attempt.
	maxDiagnosticTopologyNodeReasons = 100
)

const (
	OperationCalculateNodeOfferSlot      = "CalculateNodeOfferSlot"
	OperationCalculateNodeExistingPodNum = "CalculateNodeExistingPodsNum"
)

type NetworkTopologySolver interface {
	// PlacePods place pods on nodes according to network topology. Please assure nodes is already cloned.
	// TODO Currently, only one score is supported for each node. Subsequent algorithms may need to support calling different plugins to score nodes and support configuring plugin weights.
	PlacePods(
		ctx context.Context,
		cycleStates map[string]fwktype.CycleState,
		toSchedulePods []*corev1.Pod,
		nodes []fwktype.NodeInfo,
		addPod podFunc,
		jobNetworkRequirements *JobTopologyRequirements,
		clusterNetworkTopology *networktopology.TreeNode,
		nodeToScore map[string]int,
	) (podToNode map[string]string, status *fwktype.Status)
}

var (
	_ NetworkTopologySolver = &networkTopologySolverImpl{}
)

type networkTopologySolverImpl struct {
	handle frameworkext.ExtendedHandle
}

func (solver *networkTopologySolverImpl) PlacePods(
	ctx context.Context,
	cycleStates map[string]fwktype.CycleState,
	toSchedulePods []*corev1.Pod,
	nodes []fwktype.NodeInfo,
	addPod podFunc,
	jobNetworkRequirements *JobTopologyRequirements,
	clusterNetworkTopology *networktopology.TreeNode,
	nodeToScore map[string]int,
) (map[string]string, *fwktype.Status) {
	topologyState := TopologyStateFromContext(ctx)
	nodeOfferSlot := solver.calculateNodeOfferSlot(ctx, cycleStates, toSchedulePods, nodes, addPod)
	nodeToExistingNums := calculateNodeExistingPodsNum(ctx, solver.handle.Parallelizer().(parallelize.Parallelizer), extension.GetPodNetworkTopologySelector(toSchedulePods[0]), nodes)
	nodeLayeredTopologyNodes := enumerateNodeTopologyNode(clusterNetworkTopology, len(nodes))
	evaluateTopologyNode(nodeLayeredTopologyNodes, nodeOfferSlot, nodeToScore, nodeToExistingNums)
	constrainOfferSlotByPodCountMultiple(clusterNetworkTopology, jobNetworkRequirements.LayerPodCountMultiple)

	topologyState.MustGatheredTopologyNode = searchMustGatherSatisfiedNodes(jobNetworkRequirements, clusterNetworkTopology)
	candidateTopologyNodes := searchOfferSlotSatisfiedNodes(jobNetworkRequirements, topologyState.MustGatheredTopologyNode)

	if len(candidateTopologyNodes) > 0 {
		sort.Slice(candidateTopologyNodes, func(i, j int) bool {
			return topologyNodeLessFunc(candidateTopologyNodes[i], candidateTopologyNodes[j], true)
		})
		for _, candidate := range candidateTopologyNodes {
			distribution := map[string]int{}
			orderedNodes, actualSlot := distributeOfferSlot(jobNetworkRequirements.DesiredOfferSlot, candidate, distribution, jobNetworkRequirements.LayerPodCountMultiple)
			if actualSlot >= jobNetworkRequirements.DesiredOfferSlot {
				podToNode := distributePods(toSchedulePods, orderedNodes, distribution)
				placements := make([]string, 0, len(toSchedulePods))
				for _, pod := range toSchedulePods {
					podKey := framework.GetNamespacedName(pod.Namespace, pod.Name)
					placements = append(placements, fmt.Sprintf("%s->%s", podKey, podToNode[podKey]))
				}
				klog.Infof("[NetworkTopology] %s/%s placed by must-gather topology on %s/%s: %s",
					toSchedulePods[0].Namespace, toSchedulePods[0].Name, candidate.Layer, candidate.Name, strings.Join(placements, ", "))
				return podToNode, nil
			}
		}
	}

	topologyNodeSummary := buildTopologyNodeSummary(topologyState.MustGatheredTopologyNode)

	// Append PodCountMultiple constraint information if present
	var podCountMultipleInfo string
	if len(jobNetworkRequirements.LayerPodCountMultiple) > 0 {
		var constraints []string
		for layer, multiple := range jobNetworkRequirements.LayerPodCountMultiple {
			constraints = append(constraints, fmt.Sprintf("%s=%d", layer, multiple))
		}
		sort.Strings(constraints)
		podCountMultipleInfo = fmt.Sprintf("; podCountMultiple constraints: %s", strings.Join(constraints, ", "))
	}

	fitError := &framework.FitError{
		NumAllNodes: len(nodes),
		Diagnosis: framework.Diagnosis{
			NodeToStatus: framework.NewNodeToStatus(topologyState.NodeToStatusMap, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable)),
		},
	}
	failureMessage := fmt.Sprintf(MessageNoCandidateTopologyNodes, jobNetworkRequirements.DesiredOfferSlot, topologyNodeSummary, fitError.Error()) + podCountMultipleInfo
	// The failure message itself already reaches the Pod status/event and, once the diagnosis
	// dump is turned on, the diagnosis as its preFilterMessage, so only the per-topology-node
	// breakdown is logged here since no other channel carries it. It is emitted at a higher
	// verbosity and bounded because a gang that cannot be placed is retried, which would
	// otherwise re-emit the whole must-gather layer on every scheduling attempt.
	if klog.V(4).Enabled() {
		klog.V(4).Infof("[NetworkTopology] %s/%s cannot be placed by must-gather topology, must-gather topology nodes: %s",
			toSchedulePods[0].Namespace, toSchedulePods[0].Name,
			strings.Join(buildDiagnosticTopologyNodeReasons(topologyState.MustGatheredTopologyNode), ";"))
	}
	return nil, fwktype.NewStatus(fwktype.Unschedulable, failureMessage)
}

// buildTopologyNodeSummary reports how many topology nodes the must-gather layer holds and
// the largest offer slot any of them can provide, which is all the job-level information
// needed to tell how far the must-gather layer is from the desired offer slot. Enumerating
// the topology nodes one by one is deliberately avoided: a large cluster may hold thousands
// of them, which makes the status/event message huge. The per-topology-node breakdown is
// emitted by the diagnostic log instead, and the underlying per-node filter failures are
// already summarized by the FitError part.
func buildTopologyNodeSummary(mustGatheredNodes []*networktopology.TreeNode) string {
	maxOfferSlot := 0
	for _, node := range mustGatheredNodes {
		if node.OfferSlot > maxOfferSlot {
			maxOfferSlot = node.OfferSlot
		}
	}
	return fmt.Sprintf("max offer slot among %d must-gather topology nodes: %d", len(mustGatheredNodes), maxOfferSlot)
}

// buildDiagnosticTopologyNodeReasons enumerates the must-gathered topology nodes with their
// offer slots for the diagnostic log, largest offer slot first so that the topology nodes
// closest to satisfying the job are never truncated away, and caps the list to keep the log
// bounded in large clusters.
func buildDiagnosticTopologyNodeReasons(mustGatheredNodes []*networktopology.TreeNode) []string {
	sortedTopologyNodes := make([]*networktopology.TreeNode, len(mustGatheredNodes))
	copy(sortedTopologyNodes, mustGatheredNodes)
	sort.Slice(sortedTopologyNodes, func(i, j int) bool {
		a, b := sortedTopologyNodes[i], sortedTopologyNodes[j]
		if a.OfferSlot != b.OfferSlot {
			return a.OfferSlot > b.OfferSlot
		}
		if a.Layer != b.Layer {
			return a.Layer < b.Layer
		}
		return a.Name < b.Name
	})
	listedNodes := len(sortedTopologyNodes)
	if listedNodes > maxDiagnosticTopologyNodeReasons {
		listedNodes = maxDiagnosticTopologyNodeReasons
	}
	reasons := make([]string, 0, listedNodes+1)
	for _, node := range sortedTopologyNodes[:listedNodes] {
		reasons = append(reasons, fmt.Sprintf("topology topologyNode %s/%s: %d", node.Layer, node.Name, node.OfferSlot))
	}
	if len(sortedTopologyNodes) > listedNodes {
		reasons = append(reasons, fmt.Sprintf("and %d more topology nodes", len(sortedTopologyNodes)-listedNodes))
	}
	return reasons
}

func (solver *networkTopologySolverImpl) calculateNodeOfferSlot(
	ctx context.Context,
	cycleStates map[string]fwktype.CycleState,
	toSchedulePods []*corev1.Pod,
	nodeInfos []fwktype.NodeInfo,
	addPod podFunc,
) map[string]int {
	topologyState := TopologyStateFromContext(ctx)
	topologyState.NodeOfferSlot = make(map[string]int, len(nodeInfos))
	topologyState.NodeToStatusMap = make(map[string]*fwktype.Status)
	var statusLock sync.RWMutex
	calculateForNode := func(nodeI int) {
		nodeInfo := nodeInfos[nodeI]
		cycleState := cycleStates[nodeInfo.Node().Name]
		var offerSlot int
		var status *fwktype.Status
		for podI := range toSchedulePods {
			toSchedulePod := toSchedulePods[podI]
			status = solver.handle.RunFilterPluginsWithNominatedPods(ctx, cycleState, toSchedulePod, nodeInfo)
			if !status.IsSuccess() {
				break
			}
			if podI+1 < len(toSchedulePods) {
				podToSchedule := toSchedulePods[podI+1]
				assumedPod := toSchedulePod.DeepCopy()
				assumedPod.Spec.NodeName = nodeInfo.Node().Name
				podInfoToAdd, _ := framework.NewPodInfo(assumedPod)
				// TODO consider pod assume on reservation
				err := addPod(cycleState, podToSchedule, podInfoToAdd, nodeInfo)
				if err != nil {
					status = fwktype.AsStatus(err)
					break
				}
			}
			offerSlot += 1
		}
		statusLock.Lock()
		topologyState.NodeOfferSlot[nodeInfo.Node().Name] = offerSlot
		if !status.IsSuccess() {
			topologyState.NodeToStatusMap[nodeInfo.Node().Name] = status
		}
		statusLock.Unlock()
	}
	solver.handle.Parallelizer().Until(ctx, len(nodeInfos), calculateForNode, OperationCalculateNodeOfferSlot)
	return topologyState.NodeOfferSlot
}

func calculateNodeExistingPodsNum(
	ctx context.Context,
	parallelizer parallelize.Parallelizer,
	selectorKey string,
	nodeInfos []fwktype.NodeInfo) map[string]int {
	if selectorKey == "" {
		return nil
	}
	nodeToExistingPodsNum := make(map[string]int, len(nodeInfos))
	var mapLock sync.RWMutex
	calculateForNode := func(nodeI int) {
		nodeInfo := nodeInfos[nodeI]
		podNum := 0
		for _, podInfo := range nodeInfo.GetPods() {
			pod := podInfo.GetPod()
			if extension.GetPodNetworkTopologySelector(pod) == selectorKey {
				podNum += 1
			}
		}
		mapLock.Lock()
		nodeToExistingPodsNum[nodeInfo.Node().Name] = podNum
		mapLock.Unlock()
	}
	parallelizer.Until(ctx, len(nodeInfos), calculateForNode, OperationCalculateNodeExistingPodNum)
	return nodeToExistingPodsNum
}

func enumerateNodeTopologyNode(
	clusterNetworkTopology *networktopology.TreeNode,
	nodesNum int,
) map[string]*networktopology.TreeNode {
	nodeToTopologyNodes := make(map[string]*networktopology.TreeNode, nodesNum)
	layeredTopologyNodes := []*networktopology.TreeNode{clusterNetworkTopology}
	for len(layeredTopologyNodes) > 0 {
		var nextLayeredTopologyNodes []*networktopology.TreeNode
		for _, layeredTopologyNode := range layeredTopologyNodes {
			if layeredTopologyNode.Layer == schedulingv1alpha1.NodeTopologyLayer {
				nodeToTopologyNodes[layeredTopologyNode.Name] = layeredTopologyNode
				continue
			}
			for _, childNode := range layeredTopologyNode.Children {
				if childNode == nil {
					break
				}
				nextLayeredTopologyNodes = append(nextLayeredTopologyNodes, childNode)
			}
		}
		layeredTopologyNodes = nextLayeredTopologyNodes
	}
	return nodeToTopologyNodes
}

func evaluateTopologyNode(
	nodeToTopologyNodes map[string]*networktopology.TreeNode,
	nodeOfferSlot map[string]int,
	nodeToScore map[string]int,
	nodeExitingPodsNum map[string]int,
) {
	for nodeName, offerSlot := range nodeOfferSlot {
		topologyNode := nodeToTopologyNodes[nodeName]
		for topologyNode != nil {
			topologyNode.OfferSlot += offerSlot
			topologyNode.Score += nodeToScore[nodeName]
			topologyNode = topologyNode.Parent
		}
	}
	for nodeName, existingPodNum := range nodeExitingPodsNum {
		topologyNode := nodeToTopologyNodes[nodeName]
		for topologyNode != nil {
			topologyNode.ExistingPodNum += existingPodNum
			topologyNode = topologyNode.Parent
		}
	}
}

// constrainOfferSlotByPodCountMultiple traverses the topology tree bottom-up
// and constrains each node's OfferSlot based on PodCountMultiple requirements.
// After this, OfferSlot at each node reflects the maximum achievable capacity
// considering PodCountMultiple constraints.
func constrainOfferSlotByPodCountMultiple(
	root *networktopology.TreeNode,
	layerPodCountMultiple map[schedulingv1alpha1.TopologyLayer]int,
) {
	if len(layerPodCountMultiple) == 0 {
		return
	}
	doConstrainOfferSlot(root, layerPodCountMultiple)
}

func doConstrainOfferSlot(
	node *networktopology.TreeNode,
	layerPodCountMultiple map[schedulingv1alpha1.TopologyLayer]int,
) {
	if node.Layer == schedulingv1alpha1.NodeTopologyLayer {
		if multiple := layerPodCountMultiple[node.Layer]; multiple > 1 {
			node.OfferSlot = (node.OfferSlot / multiple) * multiple
		}
		return
	}
	constrainedSum := 0
	for _, child := range node.Children {
		if child != nil {
			doConstrainOfferSlot(child, layerPodCountMultiple)
			constrainedSum += child.OfferSlot
		}
	}
	node.OfferSlot = constrainedSum
	if multiple := layerPodCountMultiple[node.Layer]; multiple > 1 {
		node.OfferSlot = (node.OfferSlot / multiple) * multiple
	}
}

func searchMustGatherSatisfiedNodes(
	jobNetworkRequirements *JobTopologyRequirements,
	clusterNetworkTopology *networktopology.TreeNode,
) []*networktopology.TreeNode {
	topologyLayerMustGather := jobNetworkRequirements.TopologyLayerMustGather
	if topologyLayerMustGather == "" {
		return []*networktopology.TreeNode{clusterNetworkTopology}
	}
	mustGatherSatisfied := false
	var mustGatherSatisfiedNodes []*networktopology.TreeNode
	layeredTopologyNodes := []*networktopology.TreeNode{clusterNetworkTopology}
	for !mustGatherSatisfied && len(layeredTopologyNodes) > 0 {
		var nextLayeredTopologyNodes []*networktopology.TreeNode
		for _, layeredTopologyNode := range layeredTopologyNodes {
			if layeredTopologyNode.Layer == topologyLayerMustGather {
				mustGatherSatisfied = true
				mustGatherSatisfiedNodes = append(mustGatherSatisfiedNodes, layeredTopologyNode)
				continue
			}
			for _, childNode := range layeredTopologyNode.Children {
				if childNode == nil {
					break
				}
				nextLayeredTopologyNodes = append(nextLayeredTopologyNodes, childNode)
			}
		}
		layeredTopologyNodes = nextLayeredTopologyNodes
	}
	return mustGatherSatisfiedNodes
}

func searchOfferSlotSatisfiedNodes(
	jobNetworkRequirements *JobTopologyRequirements,
	mustGatherSatisfiedNodes []*networktopology.TreeNode,
) []*networktopology.TreeNode {
	desiredOfferSlot := jobNetworkRequirements.DesiredOfferSlot
	var candidates []*networktopology.TreeNode
	layeredTopologyNodes := make([]*networktopology.TreeNode, len(mustGatherSatisfiedNodes))
	copy(layeredTopologyNodes, mustGatherSatisfiedNodes)
	for len(layeredTopologyNodes) > 0 {
		var nextLayeredTopologyNodes []*networktopology.TreeNode
		var layeredCandidates []*networktopology.TreeNode
		for _, layeredTopologyNode := range layeredTopologyNodes {
			if layeredTopologyNode.OfferSlot < desiredOfferSlot {
				continue
			}

			layeredCandidates = append(layeredCandidates, layeredTopologyNode)
			for _, child := range layeredTopologyNode.Children {
				if child != nil {
					nextLayeredTopologyNodes = append(nextLayeredTopologyNodes, child)
				}
			}
		}
		if len(layeredCandidates) > 0 {
			candidates = layeredCandidates
		}
		layeredTopologyNodes = nextLayeredTopologyNodes
	}
	return candidates
}

var topologyNodeLessFunc = func(a, b *networktopology.TreeNode, lowerOfferSlot bool) bool {
	// Compare ExistingPodNum layer by layer from the current node
	for nodeA, nodeB := a, b; nodeA != nil && nodeB != nil; nodeA, nodeB = nodeA.Parent, nodeB.Parent {
		if nodeA.ExistingPodNum != nodeB.ExistingPodNum {
			return nodeA.ExistingPodNum > nodeB.ExistingPodNum
		}
	}
	// Compare OfferSlot layer by layer from the current node
	for nodeA, nodeB := a, b; nodeA != nil && nodeB != nil; nodeA, nodeB = nodeA.Parent, nodeB.Parent {
		if nodeA.OfferSlot != nodeB.OfferSlot {
			return (nodeA.OfferSlot < nodeB.OfferSlot) == lowerOfferSlot
		}
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Name < b.Name
}

func distributeOfferSlot(
	desiredOfferSlot int,
	topologyNode *networktopology.TreeNode,
	distribution map[string]int,
	layerPodCountMultiple map[schedulingv1alpha1.TopologyLayer]int,
) (topologyOrderedNodes []string, offerSlot int) {
	// Calculate the maximum slot this topology node can provide

	maxOfferSlot := topologyNode.OfferSlot
	if maxOfferSlot > desiredOfferSlot {
		maxOfferSlot = desiredOfferSlot
	}

	if multiple := layerPodCountMultiple[topologyNode.Layer]; multiple > 1 {
		maxOfferSlot = (maxOfferSlot / multiple) * multiple
	}

	if topologyNode.Layer == schedulingv1alpha1.NodeTopologyLayer {
		distribution[topologyNode.Name] = maxOfferSlot
		return []string{topologyNode.Name}, maxOfferSlot
	}

	var children []*networktopology.TreeNode
	for _, child := range topologyNode.Children {
		if child != nil {
			children = append(children, child)
		}
	}
	sort.Slice(children, func(i, j int) bool {
		return topologyNodeLessFunc(children[i], children[j], false)
	})

	remainingSlot := maxOfferSlot
	for _, child := range children {
		orderedNodesOfChild, offerSlotOfChild := distributeOfferSlot(remainingSlot, child, distribution, layerPodCountMultiple)
		topologyOrderedNodes = append(topologyOrderedNodes, orderedNodesOfChild...)
		remainingSlot -= offerSlotOfChild
		offerSlot += offerSlotOfChild
	}
	return topologyOrderedNodes, offerSlot
}

func distributePods(
	toSchedulePods []*corev1.Pod,
	topologyOrderedNodes []string,
	nodeToOfferSlot map[string]int,
) map[string]string {
	sort.Slice(toSchedulePods, func(i, j int) bool {
		return toSchedulePods[i].Name < toSchedulePods[j].Name
	})
	podToNode := make(map[string]string, len(toSchedulePods))
	currentNodeIndex := 0
	for _, pod := range toSchedulePods {
		currentNode := topologyOrderedNodes[currentNodeIndex]
		offerSlot := nodeToOfferSlot[currentNode]
		for offerSlot <= 0 {
			currentNodeIndex++
			currentNode = topologyOrderedNodes[currentNodeIndex]
			offerSlot = nodeToOfferSlot[currentNode]
		}
		podToNode[framework.GetNamespacedName(pod.Namespace, pod.Name)] = currentNode
		offerSlot--
		nodeToOfferSlot[currentNode] = offerSlot
	}
	return podToNode
}

func NewNetworkTopologySolver(handle fwktype.Handle) NetworkTopologySolver {
	return &networkTopologySolverImpl{
		handle: handle.(frameworkext.ExtendedHandle),
	}
}
