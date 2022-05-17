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

package loadaware

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	slolisters "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	Name = "LoadAwareScheduling"
)

var (
	_ framework.FilterPlugin  = &Plugin{}
	_ framework.ScorePlugin   = &Plugin{}
	_ framework.ReservePlugin = &Plugin{}
)

type Plugin struct {
	handle           framework.Handle
	args             *config.LoadAwareSchedulingArgs
	nodeMetricLister slolisters.NodeMetricLister
	podAssignCache   *podAssignCache
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*config.LoadAwareSchedulingArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type LoadAwareSchedulingArgs, got %T", args)
	}

	if err := validation.ValidateLoadAwareSchedulingArgs(pluginArgs); err != nil {
		return nil, err
	}

	frameworkExtender, ok := handle.(*frameworkext.FrameworkExtender)
	if !ok {
		return nil, fmt.Errorf("want handle to be of type frameworkext.FrameworkExtender, got %T", handle)
	}

	podAssignCache := newPodAssignCache()
	frameworkExtender.SharedInformerFactory().Core().V1().Pods().Informer().AddEventHandler(podAssignCache)
	nodeMetricLister := frameworkExtender.KoordinatorSharedInformerFactory().Slo().V1alpha1().NodeMetrics().Lister()

	return &Plugin{
		handle:           handle,
		args:             pluginArgs,
		nodeMetricLister: nodeMetricLister,
		podAssignCache:   podAssignCache,
	}, nil
}

func (p *Plugin) Name() string { return Name }

func (p *Plugin) Filter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	nodeMetric, err := p.nodeMetricLister.Get(node.Name)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	if p.args.FilterUnhealthyNodeMetrics {
		if isNodeMetricUnhealthy(nodeMetric, p.args.NodeMetricUpdateMaxWindowSeconds) {
			return framework.NewStatus(framework.Unschedulable, "node(s) nodeMetric unhealthy")
		}
	}

	if len(p.args.UsageThresholds) > 0 {
		if nodeMetric.Status.NodeMetric == nil {
			return nil
		}
		for resourceName, threshold := range p.args.UsageThresholds {
			total := node.Status.Allocatable[resourceName]
			if total.IsZero() {
				continue
			}
			used := nodeMetric.Status.NodeMetric.NodeUsage.ResourceList[resourceName]
			usage := used.MilliValue() / total.MilliValue()
			if usage >= threshold {
				return framework.NewStatus(framework.Unschedulable, fmt.Sprintf("node(s) %s usage exceed threshold", resourceName))
			}
		}
	}

	return nil
}

func isNodeMetricUnhealthy(nodeMetric *slov1alpha1.NodeMetric, nodeMetricUpdateMaxWindowSeconds int64) bool {
	return nodeMetric == nil ||
		nodeMetric.Status.UpdateTime == nil ||
		time.Since(nodeMetric.Status.UpdateTime.Time) >= time.Duration(nodeMetricUpdateMaxWindowSeconds)*time.Second
}

func (p *Plugin) Score(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	nodeMetric, err := p.nodeMetricLister.Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, "nodeMetric not found")
	}
	if isNodeMetricUnhealthy(nodeMetric, p.args.NodeMetricUpdateMaxWindowSeconds) {
		return 0, nil
	}

	return 0, nil
}

func estimatedPodUsage(pod *corev1.Pod, resourceNames []corev1.ResourceName, scalingFactors map[corev1.ResourceName]int64) corev1.ResourceList {
	request, limit := resourceapi.PodRequestsAndLimits(pod)

	estimatedUsage := make(corev1.ResourceList)
	priorityClass := extension.GetPriorityClass(pod)
	for _, resourceName := range resourceNames {
		realResourceName := extension.TranslateResourceNameByPriorityClass(priorityClass, resourceName)
		quantity := request[realResourceName]
		if quantity.IsZero() {
			continue
		}
		switch realResourceName {
		case extension.BatchCPU:
			quantity.SetMilli(quantity.MilliValue() / 1000 * scalingFactors[resourceName])
		case corev1.ResourceCPU:
			quantity.SetMilli(quantity.MilliValue() * scalingFactors[resourceName])
		default:
			quantity.Set(quantity.Value() * scalingFactors[resourceName])
		}
		estimatedUsage[resourceName] = quantity
	}

	return estimatedUsage
}

func getResourceQuantity(request, limit corev1.ResourceList, resourceName corev1.ResourceName, scalingFactor int64) resource.Quantity {
	if quantity, ok := limit[resourceName]; ok && quantity.IsZero() {
		return extension.TranslateToResourceQuantity(quantity, resourceName)
	}
	quantity := request[resourceName]
	if !quantity.IsZero() {

	}
	return extension.TranslateToResourceQuantity(quantity, resourceName)
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func (p *Plugin) Reserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	p.podAssignCache.assign(nodeName, pod)
	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	p.podAssignCache.unAssign(nodeName, pod)
}
