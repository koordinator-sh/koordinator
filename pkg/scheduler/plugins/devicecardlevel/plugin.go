package devicecardlevel

import (
	"context"
	"github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	Name = "NodeDevicePlugin"
)

var (
	_ framework.FilterPlugin  = &NodeDevicePlugin{}
	_ framework.ReservePlugin = &NodeDevicePlugin{}
	_ framework.PreBindPlugin = &NodeDevicePlugin{}
)

type NodeDevicePlugin struct {
	frameworkHandler framework.Handle
	//deviceClient		deviceClient.Interface
	deviceLister    v1alpha1.DeviceLister
	nodeLister      framework.NodeInfoLister
	NodeDeviceCache *NodeDeviceCache
}

func (p *NodeDevicePlugin) Filter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	//TODO implement me
	panic("implement me")
}

func (p *NodeDevicePlugin) Reserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	//TODO implement me
	panic("implement me")
}

func (p *NodeDevicePlugin) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	//TODO implement me
	panic("implement me")
}

func (p *NodeDevicePlugin) PreBind(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	//TODO implement me
	panic("implement me")
}

func (p *NodeDevicePlugin) Name() string {
	return Name
}
