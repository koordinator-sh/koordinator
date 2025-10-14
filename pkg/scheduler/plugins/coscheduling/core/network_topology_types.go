package core

import (
	"context"
	"sort"

	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
)

type TopologyState struct {
	JobTopologyRequirements  *JobTopologyRequirements
	NodeOfferSlot            map[string]int
	NodeToStatusMap          framework.NodeToStatusMap
	MustGatheredTopologyNode []*networktopology.TreeNode
}

type ContextKey struct {
}

func TopologyStateFromContext(ctx context.Context) *TopologyState {
	return ctx.Value(ContextKey{}).(*TopologyState)
}

func ContextWithTopologyState(ctx context.Context, topologyState *TopologyState) context.Context {
	ctx = context.WithValue(ctx, ContextKey{}, topologyState)
	return ctx
}

type JobTopologyRequirements struct {
	TopologyLayerMustGather schedulingv1alpha1.TopologyLayer
	DesiredOfferSlot        int
}

func GetMustGatherLayer(spec *extension.NetworkTopologySpec, isLayerAncestorFunc networktopology.IsLayerAncestorFunc) schedulingv1alpha1.TopologyLayer {
	sort.Slice(spec.GatherStrategy, func(i, j int) bool {
		return isLayerAncestorFunc(spec.GatherStrategy[i].Layer, spec.GatherStrategy[j].Layer)
	})
	for _, rule := range spec.GatherStrategy {
		if rule.Strategy == extension.NetworkTopologyGatherStrategyMustGather {
			return rule.Layer
		}
	}
	return ""
}
