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

package impl

import (
	"context"
	"encoding/json"
	rawerrors "errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	topologyclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned"
	topologylister "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/listers/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/kubelet"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

const (
	nodeTopoInformerName PluginName = "nodeTopoInformer"

	NodeZoneType = "Node"
)

type nodeTopologyStatus struct {
	Annotations    map[string]string
	TopologyPolicy v1alpha1.TopologyManagerPolicy
	Zones          v1alpha1.ZoneList
}

func (n *nodeTopologyStatus) isChanged(oldNRT *v1alpha1.NodeResourceTopology) (bool, string) {
	if oldNRT == nil || oldNRT.Annotations == nil {
		return true, "metadata changed"
	}

	// check TopologyPolicies
	if !reflect.DeepEqual(oldNRT.TopologyPolicies, []string{string(n.TopologyPolicy)}) {
		return true, "TopologyPolicies changed"
	}

	// check annotations
	if isEqual, key := isEqualNRTAnnotations(oldNRT.Annotations, n.Annotations); !isEqual {
		return true, fmt.Sprintf("annotations changed, key %s", key)
	}

	// check Zones
	if isEqual, msg := isEqualNRTZones(oldNRT.Zones, n.Zones); !isEqual {
		return true, fmt.Sprintf("Zones changed, item: %s", msg)
	}

	return false, ""
}

func (n *nodeTopologyStatus) updateNRT(nrt *v1alpha1.NodeResourceTopology) {
	if nrt.Annotations == nil {
		nrt.Annotations = map[string]string{}
	}
	for k, v := range n.Annotations {
		nrt.Annotations[k] = v
	}

	nrt.TopologyPolicies = []string{string(n.TopologyPolicy)}

	// TBD: merge with the existing
	nrt.Zones = n.Zones
}

type nodeTopoInformer struct {
	config         *Config
	topologyClient topologyclientset.Interface
	nodeTopoMutex  sync.RWMutex
	nodeTopology   *v1alpha1.NodeResourceTopology

	metricCache    metriccache.MetricCache
	callbackRunner *callbackRunner

	nodeResourceTopologyInformer cache.SharedIndexInformer
	nodeResourceTopologyLister   topologylister.NodeResourceTopologyLister

	kubelet      KubeletStub
	nodeInformer *nodeInformer
	podsInformer *podsInformer
}

func NewNodeTopoInformer() *nodeTopoInformer {
	return &nodeTopoInformer{}
}

func (s *nodeTopoInformer) GetNodeTopo() *v1alpha1.NodeResourceTopology {
	s.nodeTopoMutex.RLock()
	defer s.nodeTopoMutex.RUnlock()
	return s.nodeTopology.DeepCopy()
}

func (s *nodeTopoInformer) Setup(ctx *PluginOption, state *PluginState) {
	s.config = ctx.config
	s.topologyClient = ctx.TopoClient
	s.metricCache = state.metricCache
	s.callbackRunner = state.callbackRunner

	s.nodeResourceTopologyInformer = newNodeResourceTopologyInformer(ctx.TopoClient, ctx.NodeName)
	s.nodeResourceTopologyLister = topologylister.NewNodeResourceTopologyLister(s.nodeResourceTopologyInformer.GetIndexer())

	nodeInformerIf := state.informerPlugins[nodeInformerName]
	nodeInformer, ok := nodeInformerIf.(*nodeInformer)
	if !ok {
		klog.Fatalf("node informer format error")
	}
	s.nodeInformer = nodeInformer

	podsInformerIf := state.informerPlugins[podsInformerName]
	podsInformer, ok := podsInformerIf.(*podsInformer)
	if !ok {
		klog.Fatalf("pods informer format error")
	}
	s.podsInformer = podsInformer
}

func (s *nodeTopoInformer) Start(stopCh <-chan struct{}) {
	klog.V(2).Infof("starting node topo informer")

	if !cache.WaitForCacheSync(stopCh, s.nodeInformer.HasSynced, s.podsInformer.HasSynced) {
		klog.Fatalf("timed out waiting for caches to sync")
	}
	if features.DefaultKoordletFeatureGate.Enabled(features.NodeTopologyReport) {
		go s.nodeResourceTopologyInformer.Run(stopCh)
		if !cache.WaitForCacheSync(stopCh, s.nodeResourceTopologyInformer.HasSynced) {
			klog.Fatalf("timed out waiting for Topology cache to sync")
		}
	}

	if s.config.NodeTopologySyncInterval <= 0 {
		return
	}

	stub, err := newKubeletStubFromConfig(s.nodeInformer.GetNode(), s.config)
	if err != nil {
		klog.Fatalf("create kubelet stub, %v", err)
	}
	s.kubelet = stub

	go wait.Until(s.reportNodeTopology, s.config.NodeTopologySyncInterval, stopCh)
	klog.V(2).Infof("node topo informer started")
}

func (s *nodeTopoInformer) HasSynced() bool {
	// TODO only node cpu info collector relies on node topo informer
	klog.V(5).Infof("nodeTopoInformer ready to start")
	if !features.DefaultKoordletFeatureGate.Enabled(features.NodeTopologyReport) {
		return true
	}
	if s.nodeResourceTopologyInformer == nil {
		return false
	}
	synced := s.nodeResourceTopologyInformer.HasSynced()
	klog.V(5).Infof("node Topo informer has synced %v", synced)
	return synced
}

func newNodeResourceTopologyInformer(client topologyclientset.Interface, nodeName string) cache.SharedIndexInformer {
	tweakListOptionsFunc := func(opt *metav1.ListOptions) {
		opt.FieldSelector = "metadata.name=" + nodeName
	}

	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				tweakListOptionsFunc(&options)
				return client.TopologyV1alpha1().NodeResourceTopologies().List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				tweakListOptionsFunc(&options)
				return client.TopologyV1alpha1().NodeResourceTopologies().Watch(context.TODO(), options)
			},
		},
		&v1alpha1.NodeResourceTopology{},
		time.Hour*12,
		cache.Indexers{},
	)
}

func (s *nodeTopoInformer) createNodeTopoIfNotExist() {
	node := s.nodeInformer.GetNode()
	topologyName := node.Name
	ctx := context.TODO()

	_, err := s.nodeResourceTopologyLister.Get(topologyName)
	if err == nil {
		return
	}
	if !errors.IsNotFound(err) {
		klog.Errorf("failed to get NodeResourceTopology %s, err: %v", topologyName, err)
		return
	}

	topo := newNodeTopo(node)
	// TODO: add retry if create fail
	_, err = s.topologyClient.TopologyV1alpha1().NodeResourceTopologies().Create(ctx, topo, metav1.CreateOptions{})
	if err != nil {
		klog.Errorf("failed to create NodeResourceTopology %s, err: %v", topologyName, err)
		return
	}
}

// calcNodeTopo returns the calculated annotations, zone list, topology policy, error.
func (s *nodeTopoInformer) calcNodeTopo() (*nodeTopologyStatus, error) {
	nodeCPUInfo, cpuTopology, sharedPoolCPUs, err := s.calCPUTopology()
	if err != nil {
		return nil, fmt.Errorf("failed to calculate cpu topology, err: %v", err)
	}

	zoneList, err := s.calTopologyZoneList(nodeCPUInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate topology ZoneList, err: %v", err)
	}

	nodeTopoStatus := &nodeTopologyStatus{
		TopologyPolicy: v1alpha1.None,
		Zones:          zoneList,
	}

	var cpuManagerPolicy extension.KubeletCPUManagerPolicy
	topo := kubelet.NewCPUTopology((*koordletutil.LocalCPUInfo)(nodeCPUInfo))
	if s.config != nil && !s.config.DisableQueryKubeletConfig {
		kubeletConfiguration, err := s.kubelet.GetKubeletConfiguration()
		if err != nil {
			return nil, fmt.Errorf("failed to GetKubeletConfiguration, err: %v", err)
		}
		klog.V(5).Infof("kubelet args: %v", kubeletConfiguration)

		// default policy is none
		cpuManagerPolicy = extension.KubeletCPUManagerPolicy{
			Policy:  kubeletConfiguration.CPUManagerPolicy,
			Options: kubeletConfiguration.CPUManagerPolicyOptions,
		}

		if kubeletConfiguration.CPUManagerPolicy == string(cpumanager.PolicyStatic) {
			reservedCPUs, err := kubelet.GetStaticCPUManagerPolicyReservedCPUs(topo, kubeletConfiguration)
			if err != nil {
				klog.Errorf("Failed to GetStaticCPUManagerPolicyReservedCPUs, err: %v", err)
			}
			cpuManagerPolicy.ReservedCPUs = reservedCPUs.String()

			// NOTE: We should not remove reservedCPUs from sharedPoolCPUs to
			//  ensure that Burstable Pods (e.g. Pods request 0C but are limited to 4C)
			//  at least there are reservedCPUs available when nodes are allocated
		}

		// get NRT topology policy
		nodeTopoStatus.TopologyPolicy = getTopologyPolicy(kubeletConfiguration.TopologyManagerPolicy,
			kubeletConfiguration.TopologyManagerScope)
	}

	cpuManagerPolicyJSON, err := json.Marshal(cpuManagerPolicy)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cpu manager policy, err: %v", err)
	}

	// handle cpus reserved by annotation of node.
	node := s.nodeInformer.GetNode()
	reserved := getNodeReserved(topo, node.Annotations)
	reservedJson, err := json.Marshal(reserved)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal reserved resource by node.annotation, error: %v", err)
	}

	// handle cpus allocated for system qos of node
	systemQOSRes, err := extension.GetSystemQOSResource(node.Annotations)
	// TODO consider define in NodeSLO for system qos, annotation on node is provided as "Syntactic Sugar", which overlaps the NodeSLO for custom-definition
	if err != nil {
		return nil, fmt.Errorf("failed to get system qos resource from node annotation, error: %v", err)
	}
	systemQOSJson, err := json.Marshal(systemQOSRes)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal system qos resource, error %v", err)
	}

	// Users can specify the kubelet RootDirectory on the host in the koordlet DaemonSet,
	// but inside koordlet it is always mounted to the path /var/lib/kubelet
	stateFilePath := kubelet.GetCPUManagerStateFilePath("/var/lib/kubelet")
	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read state file, err: %v", err)
		}
	}
	// TODO: report lse/lsr pod from cgroup
	var podAllocsJSON []byte
	if len(data) > 0 {
		podAllocs, err := s.calGuaranteedCpu(sharedPoolCPUs, string(data))
		if err != nil {
			return nil, fmt.Errorf("failed to cal GuaranteedCpu, err: %v", err)
		}
		if len(podAllocs) != 0 {
			podAllocsJSON, err = json.Marshal(podAllocs)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal pod allocs, err: %v", err)
			}
		}
	}

	cpuTopologyJSON, err := json.Marshal(cpuTopology)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cpu topology of node, err: %v", err)
	}

	sharePools := s.calCPUSharePools(sharedPoolCPUs)
	// remove cpus that already reserved by node.annotation.
	if nodeAnnoReserved, err := cpuset.Parse(reserved.ReservedCPUs); err == nil {
		sharePools = removeNodeReservedCPUs(sharePools, nodeAnnoReserved)
	}

	// remove cpus that exclusive for system qos from annotation
	sharePools = removeSystemQOSCPUs(sharePools, systemQOSRes)
	cpuSharePoolsJSON, err := json.Marshal(sharePools)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cpushare pools of node, err: %v", err)
	}

	annotations := map[string]string{}
	annotations[extension.AnnotationNodeCPUTopology] = string(cpuTopologyJSON)
	annotations[extension.AnnotationNodeCPUSharedPools] = string(cpuSharePoolsJSON)
	annotations[extension.AnnotationKubeletCPUManagerPolicy] = string(cpuManagerPolicyJSON)
	if len(podAllocsJSON) != 0 {
		annotations[extension.AnnotationNodeCPUAllocs] = string(podAllocsJSON)
	}
	if len(reservedJson) != 0 {
		annotations[extension.AnnotationNodeReservation] = string(reservedJson)
	}
	if len(systemQOSJson) != 0 {
		annotations[extension.AnnotationNodeSystemQOSResource] = string(systemQOSJson)
	}
	nodeTopoStatus.Annotations = annotations

	klog.V(6).Infof("calculate node topology status: %+v", nodeTopoStatus)
	return nodeTopoStatus, nil
}

// removeNodeReservedCPUs filter out cpus that reserved by annotation of node.
func removeNodeReservedCPUs(cpuSharePools []extension.CPUSharedPool, reservedCPUs cpuset.CPUSet) []extension.CPUSharedPool {
	newCPUSharePools := make([]extension.CPUSharedPool, len(cpuSharePools))
	for idx, val := range cpuSharePools {
		newCPUSharePools[idx] = val
	}

	for idx, pool := range cpuSharePools {
		originCPUs, err := cpuset.Parse(pool.CPUSet)
		if err != nil {
			return newCPUSharePools
		}

		newCPUSharePools[idx].CPUSet = originCPUs.Difference(reservedCPUs).String()
	}

	return newCPUSharePools
}

// removeSystemQOSCPUs filter out cpus that for system qos.
func removeSystemQOSCPUs(cpuSharePools []extension.CPUSharedPool, sysQOSRes *extension.SystemQOSResource) []extension.CPUSharedPool {
	if sysQOSRes == nil || len(sysQOSRes.CPUSet) == 0 || !sysQOSRes.IsCPUSetExclusive() {
		// system QoS resource not specified, or cpu is not exclusive
		return cpuSharePools
	}

	systemQOSCPUs, err := cpuset.Parse(sysQOSRes.CPUSet)
	if err != nil {
		return cpuSharePools
	}

	newCPUSharePools := make([]extension.CPUSharedPool, len(cpuSharePools))
	for idx, val := range cpuSharePools {
		newCPUSharePools[idx] = val
	}

	for idx, pool := range cpuSharePools {
		originCPUs, err := cpuset.Parse(pool.CPUSet)
		if err != nil {
			return newCPUSharePools
		}

		newCPUSharePools[idx].CPUSet = originCPUs.Difference(systemQOSCPUs).String()
	}

	return newCPUSharePools
}

func getNodeReserved(cpuTopology *topology.CPUTopology, nodeAnnotations map[string]string) extension.NodeReservation {
	reserved := extension.NodeReservation{}
	reservedCPUs, numReservedCPUs := extension.GetReservedCPUs(nodeAnnotations)
	if reservedCPUs != "" {
		cpus, _ := cpuset.Parse(reservedCPUs)
		reserved.ReservedCPUs = cpus.String()
	} else if numReservedCPUs > 0 {
		allCPUs := cpuTopology.CPUDetails.CPUs()
		cpus, _ := kubelet.TakeByTopology(allCPUs, numReservedCPUs, cpuTopology)
		reserved.ReservedCPUs = cpus.String()
	}
	return reserved
}

func (s *nodeTopoInformer) calGuaranteedCpu(usedCPUs map[int32]*extension.CPUInfo, stateJSON string) ([]extension.PodCPUAlloc, error) {
	if stateJSON == "" {
		return nil, fmt.Errorf("empty state file")
	}
	checkpoint := &state.CPUManagerCheckpoint{}
	err := json.Unmarshal([]byte(stateJSON), checkpoint)
	if err != nil {
		return nil, err
	}

	pods := make(map[types.UID]*statesinformer.PodMeta)
	managedPods := make(map[types.UID]struct{})
	for _, podMeta := range s.podsInformer.GetAllPods() {
		pods[podMeta.Pod.UID] = podMeta
		qosClass := extension.GetPodQoSClassRaw(podMeta.Pod)
		if qosClass == extension.QoSLS || qosClass == extension.QoSBE {
			managedPods[podMeta.Pod.UID] = struct{}{}
			continue
		}
		resourceStatus, err := extension.GetResourceStatus(podMeta.Pod.Annotations)
		if err == nil {
			set, err := cpuset.Parse(resourceStatus.CPUSet)
			if err == nil && set.Size() > 0 {
				managedPods[podMeta.Pod.UID] = struct{}{}
			}
		}
	}

	var podAllocs []extension.PodCPUAlloc
	for podUID := range checkpoint.Entries {
		if _, ok := managedPods[types.UID(podUID)]; ok {
			continue
		}
		cpuSet := cpuset.NewCPUSet()
		for container, cpuString := range checkpoint.Entries[podUID] {
			if containerCPUSet, err := cpuset.Parse(cpuString); err != nil {
				klog.Errorf("could not parse cpuset %q for container %q in pod %q: %v", cpuString, container, podUID, err)
				continue
			} else if containerCPUSet.Size() > 0 {
				cpuSet = cpuSet.Union(containerCPUSet)
			}
		}
		if cpuSet.IsEmpty() {
			continue
		}

		// TODO: It is possible that the data in the checkpoint file is invalid
		//  and should be checked with the data in the cgroup to determine whether it is consistent
		podCPUAlloc := extension.PodCPUAlloc{
			UID:              types.UID(podUID),
			CPUSet:           cpuSet.String(),
			ManagedByKubelet: true,
		}
		podMeta := pods[types.UID(podUID)]
		if podMeta != nil {
			podCPUAlloc.Namespace = podMeta.Pod.Namespace
			podCPUAlloc.Name = podMeta.Pod.Name
		}
		podAllocs = append(podAllocs, podCPUAlloc)

		for _, cpuID := range cpuSet.ToSliceNoSort() {
			delete(usedCPUs, int32(cpuID))
		}
	}
	sort.Slice(podAllocs, func(i, j int) bool {
		return string(podAllocs[i].UID) < string(podAllocs[j].UID)
	})
	return podAllocs, nil
}

func (s *nodeTopoInformer) reportNodeTopology() {
	klog.V(4).Info("start to report node topology")
	// do not CREATE if reporting is disabled,
	// but update the node topo object internally
	isReportEnabled := features.DefaultKoordletFeatureGate.Enabled(features.NodeTopologyReport)

	// TODO: merge the create and update
	if isReportEnabled {
		s.createNodeTopoIfNotExist()
	} else {
		klog.V(5).Infof("feature %v not enabled, node topology will not be reported", features.NodeTopologyReport)
	}

	nodeTopoResult, err := s.calcNodeTopo()
	if err != nil {
		klog.Errorf("failed to calculate node topology, err: %v", err)
		return
	}

	node := s.nodeInformer.GetNode()
	err = util.RetryOnConflictOrTooManyRequests(func() error {
		var nodeResourceTopology *v1alpha1.NodeResourceTopology
		if isReportEnabled {
			nodeResourceTopology, err = s.nodeResourceTopologyLister.Get(node.Name)
			if err != nil {
				klog.Errorf("failed to get node topology, node %s, err: %v", node.Name, err)
				return err
			}
			nodeResourceTopology = nodeResourceTopology.DeepCopy() // avoid overwrite the cache
		} else {
			nodeResourceTopology = newNodeTopo(node)
		}

		isNRTChanged, msg := nodeTopoResult.isChanged(nodeResourceTopology)
		if !isNRTChanged {
			klog.V(5).Infof("all good, no need to update node topology, node %s", node.Name)
			return nil
		}
		klog.V(4).Infof("need to update node topology, node %s, reason: %s", node.Name, msg)

		// update fields
		nodeTopoResult.updateNRT(nodeResourceTopology)

		// do UPDATE
		s.updateNodeTopo(nodeResourceTopology)

		if !isReportEnabled {
			klog.V(6).Infof("skip report node topology since reporting is disabled")
			return nil
		}

		_, err = s.topologyClient.TopologyV1alpha1().NodeResourceTopologies().Update(context.TODO(), nodeResourceTopology, metav1.UpdateOptions{})
		if err != nil {
			klog.Errorf("failed to report node topology, node %s, err: %v", node.Name, err)
			return err
		}

		klog.V(6).Infof("update NodeResourceTopology successfully, %+v", nodeResourceTopology)
		return nil
	})
	if err != nil {
		klog.Errorf("failed to update NodeResourceTopology, err: %v", err)
	}
}

func isEqualNRTZones(oldZones, newZones v1alpha1.ZoneList) (bool, string) {
	if len(oldZones) != len(newZones) {
		return false, "zones number"
	}

	for i := range oldZones {
		newZone := oldZones[i]
		oldZone := newZones[i]

		if newZone.Name != oldZone.Name {
			return false, fmt.Sprintf("zone %v name", i)
		}
		if newZone.Type != oldZone.Type {
			return false, fmt.Sprintf("zone %v type", i)
		}

		if len(newZone.Resources) != len(oldZone.Resources) {
			return false, fmt.Sprintf("zone %v resources number", i)
		}
		for j := range newZone.Resources {
			newRes := newZone.Resources[j]
			oldRes := oldZone.Resources[j]
			if newRes.Name != oldRes.Name {
				return false, fmt.Sprintf("zone %v resource %v name", i, j)
			}
			if !newRes.Allocatable.Equal(oldRes.Allocatable) {
				return false, fmt.Sprintf("zone %v resource %v allocatable", i, j)
			}
			if !newRes.Capacity.Equal(oldRes.Capacity) {
				return false, fmt.Sprintf("zone %v resource %v capacity", i, j)
			}
		}
	}

	return true, ""
}

// isEqualNRTAnnotations returns whether the new topology annotations has difference with the old one or not
func isEqualNRTAnnotations(oldAnno, newAnno map[string]string) (bool, string) {
	var (
		oldData interface{}
		newData interface{}
	)
	keys := []string{
		extension.AnnotationKubeletCPUManagerPolicy,
		extension.AnnotationNodeCPUSharedPools,
		extension.AnnotationNodeCPUTopology,
		extension.AnnotationNodeCPUAllocs,
		extension.AnnotationNodeReservation,
		extension.AnnotationNodeSystemQOSResource,
	}
	for _, key := range keys {
		oldValue, oldExist := oldAnno[key]
		newValue, newExist := newAnno[key]
		if !oldExist && !newExist {
			// both not exist, no need to compare this key
			continue
		}
		if oldExist != newExist {
			// (oldExist = true, newExist = false) OR (oldExist = false, newExist = true), node topo not equal
			return false, key
		} // else both exist in new and old, compare value

		err := json.Unmarshal([]byte(oldValue), &oldData)
		if err != nil {
			klog.V(5).Infof("failed to unmarshal, key %s, err: %v", key, err)
		}
		err1 := json.Unmarshal([]byte(newValue), &newData)
		if err1 != nil {
			klog.V(5).Infof("failed to unmarshal, key %s, err: %v", key, err1)
		}
		if !reflect.DeepEqual(oldData, newData) {
			return false, key
		}
	}

	return true, ""
}

func (s *nodeTopoInformer) calCPUSharePools(sharedPoolCPUs map[int32]*extension.CPUInfo) []extension.CPUSharedPool {
	podMetas := s.podsInformer.GetAllPods()
	for _, podMeta := range podMetas {
		status, err := extension.GetResourceStatus(podMeta.Pod.Annotations)
		if err != nil {
			klog.Errorf("failed to get resource status of pod %s, err: %v", podMeta.Pod.Name, err)
			continue
		}
		if status.CPUSet == "" {
			continue
		}

		set, err := cpuset.Parse(status.CPUSet)
		if err != nil {
			klog.Errorf("failed to parse cpuset info of pod %s, err: %v", podMeta.Pod.Name, err)
			continue
		}
		for _, cpuID := range set.ToSliceNoSort() {
			delete(sharedPoolCPUs, int32(cpuID))
		}
	}

	// nodeID -> cpulist
	nodeIDToCpus := make(map[int32][]int)
	for cpuID, info := range sharedPoolCPUs {
		if info != nil {
			nodeIDToCpus[info.Node] = append(nodeIDToCpus[info.Node], int(cpuID))
		}
	}

	var sharePools []extension.CPUSharedPool
	for nodeID, cpus := range nodeIDToCpus {
		if len(cpus) <= 0 {
			continue
		}
		set := cpuset.NewCPUSet(cpus...)
		sharePools = append(sharePools, extension.CPUSharedPool{
			CPUSet: set.String(),
			Node:   nodeID,
			Socket: sharedPoolCPUs[int32(cpus[0])].Socket,
		})
	}
	sort.Slice(sharePools, func(i, j int) bool {
		iPool := sharePools[i]
		jPool := sharePools[j]
		iID := int(iPool.Socket)<<32 | int(iPool.Node)
		jID := int(jPool.Socket)<<32 | int(jPool.Node)
		return iID < jID
	})
	return sharePools
}

func (s *nodeTopoInformer) calCPUTopology() (*metriccache.NodeCPUInfo, *extension.CPUTopology, map[int32]*extension.CPUInfo, error) {
	nodeCPUInfoRaw, exist := s.metricCache.Get(metriccache.NodeCPUInfoKey)
	if !exist {
		klog.Warning("failed to get node cpu info, err: not exist")
		return nil, nil, nil, rawerrors.New("node cpu info not exist")
	}
	nodeCPUInfo, ok := nodeCPUInfoRaw.(*metriccache.NodeCPUInfo)
	if !ok {
		klog.Fatalf("type error, expect %T， but got %T", metriccache.NodeCPUInfo{}, nodeCPUInfoRaw)
	}
	cpus := make(map[int32]*extension.CPUInfo)
	cpuTopology := &extension.CPUTopology{}
	for _, cpu := range nodeCPUInfo.ProcessorInfos {
		info := extension.CPUInfo{
			ID:     cpu.CPUID,
			Core:   cpu.CoreID,
			Socket: cpu.SocketID,
			Node:   cpu.NodeID,
		}
		cpuTopology.Detail = append(cpuTopology.Detail, info)
		cpus[cpu.CPUID] = &info
	}
	sort.Slice(cpuTopology.Detail, func(i, j int) bool {
		return cpuTopology.Detail[i].ID < cpuTopology.Detail[j].ID
	})
	return nodeCPUInfo, cpuTopology, cpus, nil
}

func (s *nodeTopoInformer) calTopologyZoneList(nodeCPUInfo *metriccache.NodeCPUInfo) (v1alpha1.ZoneList, error) {
	nodeNUMAInfoRaw, exist := s.metricCache.Get(metriccache.NodeNUMAInfoKey)
	if !exist {
		klog.Warning("failed to get node NUMA info, err: not exist")
		return nil, fmt.Errorf("node cpu info not exist")
	}
	nodeNUMAInfo, ok := nodeNUMAInfoRaw.(*koordletutil.NodeNUMAInfo)
	if !ok {
		klog.Fatalf("type error, expect %T， but got %T", koordletutil.NodeNUMAInfo{}, nodeNUMAInfoRaw)
	}
	nodeNum := len(nodeNUMAInfo.NUMAInfos)

	if nodeNumFromCPUInfo := len(nodeCPUInfo.TotalInfo.NodeToCPU); nodeNumFromCPUInfo != nodeNum {
		klog.Warningf("failed to align cpu info with NUMA info, err: node number unmatched, cpu %v, NUMA %v",
			nodeNumFromCPUInfo, nodeNum)
		return nil, fmt.Errorf("NUMA node number not matched")
	}

	zoneList := make(v1alpha1.ZoneList, nodeNum)
	for i := range zoneList {
		zone := &zoneList[i]
		zone.Type = NodeZoneType
		zone.Name = makeNodeZoneName(i)

		var cpuQuant resource.Quantity
		cpuInfos, ok := nodeCPUInfo.TotalInfo.NodeToCPU[int32(i)]
		if ok {
			cpuQuant = *resource.NewQuantity(int64(len(cpuInfos)), resource.DecimalSI)
		} else {
			cpuQuant = resource.MustParse("0")
		}
		var memQuant resource.Quantity
		memInfo, ok := nodeNUMAInfo.MemInfoMap[int32(i)]
		if ok {
			memQuant = *resource.NewQuantity(int64(memInfo.MemTotalBytes()), resource.BinarySI)
		} else {
			memQuant = resource.MustParse("0")
		}

		zone.Resources = v1alpha1.ResourceInfoList{
			{
				Name:        string(corev1.ResourceCPU),
				Capacity:    cpuQuant,
				Allocatable: cpuQuant,
				Available:   cpuQuant,
			},
			{
				Name:        string(corev1.ResourceMemory),
				Capacity:    memQuant,
				Allocatable: memQuant,
				Available:   memQuant,
			},
		}
	}

	return zoneList, nil
}

func (s *nodeTopoInformer) updateNodeTopo(newTopo *v1alpha1.NodeResourceTopology) {
	s.setNodeTopo(newTopo)
	klog.V(5).Infof("local node topology info updated %v", newTopo)
	s.callbackRunner.SendCallback(statesinformer.RegisterTypeNodeTopology)
}

func (s *nodeTopoInformer) setNodeTopo(newTopo *v1alpha1.NodeResourceTopology) {
	s.nodeTopoMutex.Lock()
	defer s.nodeTopoMutex.Unlock()
	s.nodeTopology = newTopo.DeepCopy()
}

func newNodeTopo(node *corev1.Node) *v1alpha1.NodeResourceTopology {
	blocker := true
	return &v1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Name: node.Name,
			Labels: map[string]string{
				extension.LabelManagedBy: "Koordinator",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					Kind:               "Node",
					Name:               node.Name,
					UID:                node.GetUID(),
					Controller:         &blocker,
					BlockOwnerDeletion: &blocker,
				},
			},
		},
		// fields are required
		TopologyPolicies: []string{string(v1alpha1.None)},
		Zones:            v1alpha1.ZoneList{v1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
	}
}

// getTopologyPolicy gets the NRT topology policy with the kubelet topology manager policy and scope.
func getTopologyPolicy(topologyManagerPolicy string, topologyManagerScope string) v1alpha1.TopologyManagerPolicy {
	if len(topologyManagerPolicy) <= 0 {
		return v1alpha1.None
	}

	if topologyManagerScope == kubeletconfiginternal.ContainerTopologyManagerScope {
		switch topologyManagerPolicy {
		case kubeletconfiginternal.SingleNumaNodeTopologyManagerPolicy:
			return v1alpha1.SingleNUMANodeContainerLevel
		case kubeletconfiginternal.RestrictedTopologyManagerPolicy:
			return v1alpha1.RestrictedContainerLevel
		case kubeletconfiginternal.BestEffortTopologyManagerPolicy:
			return v1alpha1.BestEffortContainerLevel
		case kubeletconfiginternal.NoneTopologyManagerPolicy:
			return v1alpha1.None
		}
	} else if topologyManagerScope == kubeletconfiginternal.PodTopologyManagerScope {
		switch topologyManagerPolicy {
		case kubeletconfiginternal.SingleNumaNodeTopologyManagerPolicy:
			return v1alpha1.SingleNUMANodePodLevel
		case kubeletconfiginternal.RestrictedTopologyManagerPolicy:
			return v1alpha1.RestrictedPodLevel
		case kubeletconfiginternal.BestEffortTopologyManagerPolicy:
			return v1alpha1.BestEffortPodLevel
		case kubeletconfiginternal.NoneTopologyManagerPolicy:
			return v1alpha1.None
		}
	}

	return v1alpha1.None
}

func makeNodeZoneName(nodeID int) string {
	return fmt.Sprintf("node-%d", nodeID)
}
