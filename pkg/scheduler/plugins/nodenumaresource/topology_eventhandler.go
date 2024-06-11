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

package nodenumaresource

import (
	"context"

	nrtv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	nrtclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned"
	nrtinformers "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/informers/externalversions"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
)

type nodeResourceTopologyEventHandler struct {
	topologyManager TopologyOptionsManager
}

func registerNodeResourceTopologyEventHandler(informerFactory nrtinformers.SharedInformerFactory, topologyManager TopologyOptionsManager) error {
	nodeResTopologyInformer := informerFactory.Topology().V1alpha1().NodeResourceTopologies().Informer()
	eventHandler := &nodeResourceTopologyEventHandler{
		topologyManager: topologyManager,
	}
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), informerFactory, nodeResTopologyInformer, eventHandler)
	return nil
}

func initNRTInformerFactory(handle framework.Handle) (nrtinformers.SharedInformerFactory, error) {
	nrtClient, ok := handle.(nrtclientset.Interface)
	if !ok {
		kubeConfig := *handle.KubeConfig()
		kubeConfig.ContentType = runtime.ContentTypeJSON
		kubeConfig.AcceptContentTypes = runtime.ContentTypeJSON
		var err error
		nrtClient, err = nrtclientset.NewForConfig(&kubeConfig)
		if err != nil {
			return nil, err
		}
	}

	nodeResTopologyInformerFactory := nrtinformers.NewSharedInformerFactoryWithOptions(nrtClient, 0)
	return nodeResTopologyInformerFactory, nil
}

func (m *nodeResourceTopologyEventHandler) OnAdd(obj interface{}, isInInitialList bool) {
	nodeResTopology, ok := obj.(*nrtv1alpha1.NodeResourceTopology)
	if !ok {
		return
	}
	m.updateNodeResourceTopology(nil, nodeResTopology)
}

func (m *nodeResourceTopologyEventHandler) OnUpdate(oldObj, newObj interface{}) {
	oldNodeResTopology, ok := oldObj.(*nrtv1alpha1.NodeResourceTopology)
	if !ok {
		return
	}

	nodeResTopology, ok := newObj.(*nrtv1alpha1.NodeResourceTopology)
	if !ok {
		return
	}
	m.updateNodeResourceTopology(oldNodeResTopology, nodeResTopology)
}

func (m *nodeResourceTopologyEventHandler) OnDelete(obj interface{}) {
	var nodeResTopology *nrtv1alpha1.NodeResourceTopology
	switch t := obj.(type) {
	case *nrtv1alpha1.NodeResourceTopology:
		nodeResTopology = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		nodeResTopology, ok = t.Obj.(*nrtv1alpha1.NodeResourceTopology)
		if !ok {
			return
		}
	default:
		break
	}

	if nodeResTopology == nil {
		return
	}
	m.topologyManager.Delete(nodeResTopology.Name)
}

func (m *nodeResourceTopologyEventHandler) updateNodeResourceTopology(oldNodeResTopology, newNodeResTopology *nrtv1alpha1.NodeResourceTopology) {
	topologyOpts := NewTopologyOptions(newNodeResTopology)

	nodeName := newNodeResTopology.Name
	m.topologyManager.UpdateTopologyOptions(nodeName, func(options *TopologyOptions) {
		// Give other plugins a chance to customize a different MaxRefCount
		topologyOpts.MaxRefCount = options.MaxRefCount
		*options = topologyOpts
	})
}
