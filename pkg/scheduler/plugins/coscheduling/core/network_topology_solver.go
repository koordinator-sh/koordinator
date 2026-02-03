package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
)

const (
	MessageNoCandidateTopologyNodes = "no candidate topology nodes can accommodate job, desiredOfferSlot: %d, %s, %s"
)

const (
	OperationCalculateNodeOfferSlot = "CalculateNodeOfferSlot"
)

type NetworkTopologySolver interface {
	// PlacePods place pods on nodes according to network topology. Please assure nodes is already cloned.
	// TODO Currently, only one score is supported for each node. Subsequent algorithms may need to support calling different plugins to score nodes and support configuring plugin weights.
	PlacePods(
		ctx context.Context,
		cycleStates map[string]*framework.CycleState,
		toSchedulePods []*corev1.Pod,
		nodes []*framework.NodeInfo,
		addPod podFunc,
		jobNetworkRequirements *JobTopologyRequirements,
		clusterNetworkTopology *networktopology.TreeNode,
		nodeToScore map[string]int,
	) (podToNode map[string]string, status *framework.Status)
}

var (
	_ NetworkTopologySolver = &networkTopologySolverImpl{}
)

type networkTopologySolverImpl struct {
	handle frameworkext.ExtendedHandle
}

func (solver *networkTopologySolverImpl) PlacePods(
	ctx context.Context,
	cycleStates map[string]*framework.CycleState,
	toSchedulePods []*corev1.Pod,
	nodes []*framework.NodeInfo,
	addPod podFunc,
	jobNetworkRequirements *JobTopologyRequirements,
	clusterNetworkTopology *networktopology.TreeNode,
	nodeToScore map[string]int,
) (map[string]string, *framework.Status) {
	topologyState := TopologyStateFromContext(ctx)
	nodeOfferSlot := solver.calculateNodeOfferSlot(ctx, cycleStates, toSchedulePods, nodes, addPod)
	nodeLayeredTopologyNodes := enumerateNodeTopologyNode(clusterNetworkTopology, len(nodes))
	evaluateTopologyNode(nodeLayeredTopologyNodes, nodeOfferSlot, nodeToScore)

	topologyState.MustGatheredTopologyNode = searchMustGatherSatisfiedNodes(jobNetworkRequirements, clusterNetworkTopology)
	candidateTopologyNodes := searchOfferSlotSatisfiedNodes(jobNetworkRequirements, topologyState.MustGatheredTopologyNode)
	if len(candidateTopologyNodes) == 0 {
		var reasons []string
		for _, node := range topologyState.MustGatheredTopologyNode {
			reasons = append(reasons, fmt.Sprintf("topology topologyNode %s/%s: %d", node.Layer, node.Name, node.OfferSlot))
		}
		sort.Strings(reasons)
		fitError := &framework.FitError{
			NumAllNodes: len(nodes),
			Diagnosis: framework.Diagnosis{
				NodeToStatusMap: topologyState.NodeToStatusMap,
			},
		}
		return nil, framework.NewStatus(framework.Unschedulable, fmt.Sprintf(MessageNoCandidateTopologyNodes, jobNetworkRequirements.DesiredOfferSlot, strings.Join(reasons, ";"), fitError.Error()))
	}

	sort.Slice(candidateTopologyNodes, func(i, j int) bool {
		return topologyNodeLessFunc(candidateTopologyNodes[i], candidateTopologyNodes[j], true)
	})

	distribution := map[string]int{}
	orderedNodes, _ := distributeOfferSlot(jobNetworkRequirements.DesiredOfferSlot, candidateTopologyNodes[0], distribution)

	podToNode := distributePods(toSchedulePods, orderedNodes, distribution)
	return podToNode, nil
}

func (solver *networkTopologySolverImpl) calculateNodeOfferSlot(
	ctx context.Context,
	cycleStates map[string]*framework.CycleState,
	toSchedulePods []*corev1.Pod,
	nodeInfos []*framework.NodeInfo,
	addPod podFunc,
) map[string]int {
	topologyState := TopologyStateFromContext(ctx)
	topologyState.NodeOfferSlot = make(map[string]int, len(nodeInfos))
	topologyState.NodeToStatusMap = make(framework.NodeToStatusMap)
	var statusLock sync.RWMutex
	calculateForNode := func(nodeI int) {
		nodeInfo := nodeInfos[nodeI]
		cycleState := cycleStates[nodeInfo.Node().Name]
		var offerSlot int
		var status *framework.Status
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
					status = framework.AsStatus(err)
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
) {
	for nodeName, offerSlot := range nodeOfferSlot {
		topologyNode := nodeToTopologyNodes[nodeName]
		for topologyNode != nil {
			topologyNode.OfferSlot += offerSlot
			topologyNode.Score += nodeToScore[nodeName]
			topologyNode = topologyNode.Parent
		}
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
	mustGatherSatisfiedNodes []*networktopology.TreeNode) []*networktopology.TreeNode {
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
) (topologyOrderedNodes []string, offerSlot int) {
	if topologyNode.Layer == schedulingv1alpha1.NodeTopologyLayer {
		offerSlot = topologyNode.OfferSlot
		if offerSlot > desiredOfferSlot {
			offerSlot = desiredOfferSlot
		}
		distribution[topologyNode.Name] = offerSlot
		return []string{topologyNode.Name}, offerSlot
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
	for _, child := range children {
		orderedNodesOfChild, offerSlotOfChild := distributeOfferSlot(desiredOfferSlot, child, distribution)
		topologyOrderedNodes = append(topologyOrderedNodes, orderedNodesOfChild...)
		desiredOfferSlot -= offerSlotOfChild
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

func NewNetworkTopologySolver(handle framework.Handle) NetworkTopologySolver {
	return &networkTopologySolverImpl{
		handle: handle.(frameworkext.ExtendedHandle),
	}
}
