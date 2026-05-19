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

package arbitrator

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	k8sfwk "k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	Name = "ConflictArbitrator"
)

var (
	_ fwktype.ReservePlugin = &Plugin{}
	_ fwktype.PreBindPlugin = &Plugin{}
)

type Plugin struct {
	handle        fwktype.Handle
	schedulerName string
	rpcClient     *ArbitratorRPCClient
}

func New(_ context.Context, _ runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
	schedulerName := "koord-scheduler"
	if fwk, ok := handle.(k8sfwk.Framework); ok {
		schedulerName = fwk.ProfileName()
	}

	role := os.Getenv("KOORD_ARBITRATOR_ROLE")
	var rpcClient *ArbitratorRPCClient
	if role == "server" {
		cache := GetGlobalArbitratorCache()
		port := os.Getenv("KOORD_ARBITRATOR_PORT")
		if port == "" {
			port = "8081"
		}
		StartArbitratorRPCServer(port, handle, cache)
	} else if role == "client" {
		serverURL := os.Getenv("KOORD_ARBITRATOR_URL")
		if serverURL != "" {
			rpcClient = NewArbitratorRPCClient(serverURL)
			klog.Infof("ConflictArbitrator initialized as RPC Client to %s", serverURL)
		}
	}

	return &Plugin{
		handle:        handle,
		schedulerName: schedulerName,
		rpcClient:     rpcClient,
	}, nil
}

func (p *Plugin) Name() string {
	return Name
}

func (p *Plugin) Reserve(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	if p.rpcClient != nil {
		if err := p.rpcClient.Reserve(pod, nodeName, p.schedulerName); err != nil {
			klog.Warningf("Conflict detected (RPC) for pod %s/%s on node %s: %v", pod.Namespace, pod.Name, nodeName, err)
			return fwktype.NewStatus(fwktype.Unschedulable, err.Error())
		}
		return nil
	}

	nodeInfoSnapshot, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return fwktype.NewStatus(fwktype.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}
	node := nodeInfoSnapshot.Node()
	if node == nil {
		return fwktype.NewStatus(fwktype.Error, "node not found")
	}

	nodeInfoStruct, ok := nodeInfoSnapshot.(*k8sfwk.NodeInfo)
	if !ok {
		return fwktype.NewStatus(fwktype.Error, "nodeInfoSnapshot is not *k8sfwk.NodeInfo")
	}

	cache := GetGlobalArbitratorCache()
	if err := cache.ReserveProposal(pod, nodeInfoStruct, p.schedulerName); err != nil {
		klog.Warningf("Conflict detected for pod %s/%s on node %s: %v", pod.Namespace, pod.Name, nodeName, err)
		return fwktype.NewStatus(fwktype.Unschedulable, err.Error())
	}

	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) {
	if p.rpcClient != nil {
		p.rpcClient.Unreserve(pod.UID)
		return
	}

	cache := GetGlobalArbitratorCache()
	cache.ReleaseProposal(pod.UID)
}

func (p *Plugin) PreBindPreFlight(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	return nil
}

func (p *Plugin) PreBind(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	if p.rpcClient != nil {
		if err := p.rpcClient.PreBind(pod.UID, p.schedulerName); err != nil {
			return fwktype.NewStatus(fwktype.Unschedulable, err.Error())
		}
		return nil
	}

	cache := GetGlobalArbitratorCache()
	cache.lock.RLock()
	defer cache.lock.RUnlock()

	alloc, ok := cache.allocations[pod.UID]
	if !ok {
		return fwktype.NewStatus(fwktype.Error, "arbitration proposal not found during pre-bind")
	}

	if alloc.SchedulerName != p.schedulerName {
		return fwktype.NewStatus(fwktype.Unschedulable, fmt.Sprintf("arbitration check failed: expected scheduler %s, got %s", alloc.SchedulerName, p.schedulerName))
	}

	return nil
}
