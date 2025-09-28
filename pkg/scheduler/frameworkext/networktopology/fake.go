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

package networktopology

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

const (
	FakeSpineLabel = "network.topology.nvidia.com/spine"
	FakeBlockLabel = "network.topology.nvidia.com/block"
)

var FakeClusterNetworkTopology = func() *schedulingv1alpha1.ClusterNetworkTopology {
	rawTopology := `
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: ClusterNetworkTopology
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"scheduling.koordinator.sh/v1alpha1","kind":"ClusterNetworkTopology","metadata":{"annotations":{},"name":"default"},"spec":{"networkTopologySpec":[{"labelKey":["network.topology.nvidia.com/spine"],"topologyLayer":"SpineLayer"},{"labelKey":["network.topology.nvidia.com/block"],"parentTopologyLayer":"SpineLayer","topologyLayer":"BlockLayer"},{"parentTopologyLayer":"BlockLayer","topologyLayer":"NodeTopologyLayer"}]}}
  creationTimestamp: "2025-10-09T07:55:46Z"
  generation: 1
  name: default
  resourceVersion: "532404"
  uid: 4fb21962-585c-41fb-8cb3-255348694946
spec:
  networkTopologySpec:
  - labelKey:
    - network.topology.nvidia.com/spine
    topologyLayer: SpineLayer
  - labelKey:
    - network.topology.nvidia.com/block
    parentTopologyLayer: SpineLayer
    topologyLayer: BlockLayer
  - parentTopologyLayer: BlockLayer
    topologyLayer: NodeTopologyLayer
status:
  detailStatus:
  - childTopologyLayer: SpineLayer
    childTopologyNames:
    - s1
    - s2
    nodeNum: 8
    topologyInfo:
      topologyLayer: ClusterTopologyLayer
      topologyName: ""
  - childTopologyLayer: BlockLayer
    childTopologyNames:
    - b1
    - b2
    nodeNum: 4
    parentTopologyInfo:
      topologyLayer: ClusterTopologyLayer
      topologyName: ""
    topologyInfo:
      topologyLayer: SpineLayer
      topologyName: s1
  - childTopologyLayer: NodeTopologyLayer
    nodeNum: 2
    parentTopologyInfo:
      topologyLayer: SpineLayer
      topologyName: s1
    topologyInfo:
      topologyLayer: BlockLayer
      topologyName: b1
  - childTopologyLayer: NodeTopologyLayer
    nodeNum: 2
    parentTopologyInfo:
      topologyLayer: SpineLayer
      topologyName: s1
    topologyInfo:
      topologyLayer: BlockLayer
      topologyName: b2
  - childTopologyLayer: BlockLayer
    childTopologyNames:
    - b3
    - b4
    nodeNum: 4
    parentTopologyInfo:
      topologyLayer: ClusterTopologyLayer
      topologyName: ""
    topologyInfo:
      topologyLayer: SpineLayer
      topologyName: s2
  - childTopologyLayer: NodeTopologyLayer
    nodeNum: 2
    parentTopologyInfo:
      topologyLayer: SpineLayer
      topologyName: s2
    topologyInfo:
      topologyLayer: BlockLayer
      topologyName: b3
  - childTopologyLayer: NodeTopologyLayer
    nodeNum: 2
    parentTopologyInfo:
      topologyLayer: SpineLayer
      topologyName: s2
    topologyInfo:
      topologyLayer: BlockLayer
      topologyName: b4
`
	clusterNetworkTopology := schedulingv1alpha1.ClusterNetworkTopology{}
	_ = yaml.Unmarshal([]byte(rawTopology), &clusterNetworkTopology)
	return &clusterNetworkTopology
}()

type FakeTools struct {
	InformerFactory      informers.SharedInformerFactory
	KoordInformerFactory koordinatorinformers.SharedInformerFactory
	KoordClient          koordclientset.Interface
	KubeClient           clientset.Interface
}

func NewFakeTreeManager(clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology, nodes []*corev1.Node) (TreeManager, FakeTools) {
	clientSet := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
	nodeStore := informerFactory.Core().V1().Nodes().Informer().GetStore()
	for i := range nodes {
		_, _ = clientSet.CoreV1().Nodes().Create(context.TODO(), nodes[i], metav1.CreateOptions{})
		_ = nodeStore.Add(nodes[i])
	}
	koordClientSet := koordfake.NewSimpleClientset()
	koordInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	_, _ = koordClientSet.SchedulingV1alpha1().ClusterNetworkTopologies().Create(context.TODO(), clusterNetworkTopology, metav1.CreateOptions{})
	store := koordInformerFactory.Scheduling().V1alpha1().ClusterNetworkTopologies().Informer().GetStore()
	_ = store.Add(clusterNetworkTopology)
	return NewTreeManager(koordInformerFactory, informerFactory, koordClientSet), FakeTools{
		InformerFactory:      informerFactory,
		KoordInformerFactory: koordInformerFactory,
		KoordClient:          koordClientSet,
		KubeClient:           clientSet,
	}
}
