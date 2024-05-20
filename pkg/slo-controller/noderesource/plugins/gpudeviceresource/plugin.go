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

package gpudeviceresource

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const PluginName = "GPUDeviceResource"

const (
	ResetResourcesMsg  = "reset node gpu resources"
	UpdateResourcesMsg = "node gpu resources from device"
	UpdateLabelsMsg    = "node gpu labels from device"

	NeedSyncForResourceDiffMsg = "gpu resource diff is big than threshold"
	NeedSyncForGPUModelMsgFmt  = "gpu device label %s changed"
)

var client ctrlclient.Client

type Plugin struct{}

func (p *Plugin) Name() string {
	return PluginName
}

// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=core,resources=nodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=scheduling.koordinator.sh,resources=devices,verbs=get;list;watch
// +kubebuilder:rbac:groups=topology.node.k8s.io,resources=noderesourcetopologies,verbs=get;list;watch;create;update

func (p *Plugin) Setup(opt *framework.Option) error {
	client = opt.Client

	// schedulingv1alpha1.AddToScheme(opt.Scheme)
	opt.Builder = opt.Builder.Watches(&schedulingv1alpha1.Device{}, &DeviceHandler{})

	return nil
}

func (p *Plugin) NeedSync(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	for _, resourceName := range ResourceNames {
		if util.IsResourceDiff(oldNode.Status.Allocatable, newNode.Status.Allocatable, resourceName,
			*strategy.ResourceDiffThreshold) {
			klog.V(4).InfoS("need sync node since resource diff bigger than threshold", "node", newNode.Name,
				"resource", resourceName, "threshold", *strategy.ResourceDiffThreshold)
			return true, NeedSyncForResourceDiffMsg
		}
	}

	return false, ""
}

func (p *Plugin) NeedSyncMeta(_ *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	for _, label := range Labels {
		if oldNode.Labels[label] != newNode.Labels[label] {
			klog.V(4).InfoS("need sync node metadata since label change", "node", newNode.Name,
				"label", label, "old", oldNode.Labels[label], "new", newNode.Labels[label])
			return true, fmt.Sprintf(NeedSyncForGPUModelMsgFmt, label)
		}
	}

	return false, ""
}

func (p *Plugin) Prepare(_ *configuration.ColocationStrategy, node *corev1.Node, nr *framework.NodeResource) error {
	// prepare node resources
	for _, resourceName := range ResourceNames {
		if nr.Resets[resourceName] {
			delete(node.Status.Allocatable, resourceName)
			delete(node.Status.Capacity, resourceName)
			continue
		}

		q := nr.Resources[resourceName]
		if q == nil {
			// ignore missing resources
			// TBD: shall we remove the resource when some resource types are missing
			continue
		}
		node.Status.Allocatable[resourceName] = *q
		node.Status.Capacity[resourceName] = *q
	}

	// prepare node labels
	// TBD: shall we reset labels if not exist in the NR
	if nr.Labels != nil {
		if _, ok := nr.Labels[extension.LabelGPUModel]; ok {
			node.Labels[extension.LabelGPUModel] = nr.Labels[extension.LabelGPUModel]
		}
		if _, ok := nr.Labels[extension.LabelGPUDriverVersion]; ok {
			node.Labels[extension.LabelGPUDriverVersion] = nr.Labels[extension.LabelGPUDriverVersion]
		}
	}

	return nil
}

func (p *Plugin) Reset(node *corev1.Node, message string) []framework.ResourceItem {
	return nil
}

func (p *Plugin) Calculate(_ *configuration.ColocationStrategy, node *corev1.Node, _ *corev1.PodList, _ *framework.ResourceMetrics) ([]framework.ResourceItem, error) {
	if node == nil || node.Status.Allocatable == nil {
		return nil, fmt.Errorf("missing essential arguments")
	}

	// calculate device resources
	device := &schedulingv1alpha1.Device{}
	if err := client.Get(context.TODO(), types.NamespacedName{Name: node.Name, Namespace: node.Namespace}, device); err != nil {
		if !errors.IsNotFound(err) {
			klog.V(4).InfoS("failed to get device for node", "node", node.Name, "err", err)
			return nil, fmt.Errorf("failed to get device resources: %w", err)
		}

		// device not found, reset gpu resources on node
		return p.resetGPUNodeResource()
	}

	// TODO: calculate NUMA-level resources against NRT
	return p.calculate(node, device)
}

func (p *Plugin) calculate(node *corev1.Node, device *schedulingv1alpha1.Device) ([]framework.ResourceItem, error) {
	if device == nil {
		return nil, fmt.Errorf("invalid device")
	}

	// calculate gpu resources
	gpuResources := make(corev1.ResourceList)
	totalKoordGPU := resource.NewQuantity(0, resource.DecimalSI)
	for _, d := range device.Spec.Devices {
		if d.Type != schedulingv1alpha1.GPU || !d.Health {
			continue
		}
		util.AddResourceList(gpuResources, d.Resources)
		totalKoordGPU.Add(d.Resources[extension.ResourceGPUCore])
	}
	gpuResources[extension.ResourceGPU] = *totalKoordGPU
	var items []framework.ResourceItem
	// FIXME: shall we add node resources in devices but not in ResourceNames?
	for resourceName := range gpuResources {
		q := gpuResources[resourceName]
		items = append(items, framework.ResourceItem{
			Name:     resourceName,
			Quantity: &q,
			Message:  UpdateResourcesMsg,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	klog.V(5).InfoS("calculate gpu resources", "node", node.Name, "resources", gpuResources)

	// calculate labels about gpu driver and model
	if device.Labels != nil {
		items = append(items, framework.ResourceItem{
			Name: PluginName,
			Labels: map[string]string{
				extension.LabelGPUModel:         device.Labels[extension.LabelGPUModel],
				extension.LabelGPUDriverVersion: device.Labels[extension.LabelGPUDriverVersion],
			},
			Message: UpdateLabelsMsg,
		})
	}
	klog.V(5).InfoS("calculate gpu labels", "node", node.Name, "labels", device.Labels)

	return items, nil
}

func (p *Plugin) resetGPUNodeResource() ([]framework.ResourceItem, error) {
	items := make([]framework.ResourceItem, len(ResourceNames))
	// FIXME: shall we reset node resources in devices but not in ResourceNames?
	for i := range ResourceNames {
		items[i] = framework.ResourceItem{
			Name:    ResourceNames[i],
			Reset:   true,
			Message: ResetResourcesMsg,
		}
	}
	return items, nil
}
