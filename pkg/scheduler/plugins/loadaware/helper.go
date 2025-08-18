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
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func isNodeMetricExpired(nodeMetric *slov1alpha1.NodeMetric, nodeMetricExpirationSeconds int64) bool {
	return nodeMetric == nil ||
		nodeMetric.Status.UpdateTime == nil ||
		nodeMetricExpirationSeconds > 0 &&
			time.Since(nodeMetric.Status.UpdateTime.Time) >= time.Duration(nodeMetricExpirationSeconds)*time.Second
}

func getNodeMetricReportInterval(nodeMetric *slov1alpha1.NodeMetric) time.Duration {
	if nodeMetric.Spec.CollectPolicy == nil || nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds == nil {
		return DefaultNodeMetricReportInterval
	}
	return time.Duration(*nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds) * time.Second
}

func (m *nodeMetric) getTargetAggregatedUsage(aggregatedDuration metav1.Duration, aggregationType extension.AggregationType) ResourceVector {
	// If no specific period is set, the non-empty maximum period recorded by NodeMetrics will be used by default.
	// This is a default policy.
	d := aggregatedDuration.Duration
	vec := m.aggUsages[aggUsageKey{Type: aggregationType, Duration: d}]
	if vec == nil && d == 0 {
		// All values in aggregatedDuration are empty, downgrade to use the values in NodeUsage
		vec = m.nodeUsage
	}
	return vec
}

type usageThresholdsFilterProfile struct {
	UsageThresholds     ResourceVector
	ProdUsageThresholds ResourceVector
	AggregatedUsage     *aggregatedUsageFilterProfile
}

type aggregatedUsageFilterProfile struct {
	UsageThresholds         ResourceVector
	UsageAggregationType    extension.AggregationType
	UsageAggregatedDuration metav1.Duration
}

func NewUsageThresholdsFilterProfile(args *schedulingconfig.LoadAwareSchedulingArgs, vectorizer ResourceVectorizer) *usageThresholdsFilterProfile {
	p := &usageThresholdsFilterProfile{}
	if len(args.UsageThresholds) > 0 {
		p.UsageThresholds = vectorizer.ToFactorVec(args.UsageThresholds)
	}
	if len(args.ProdUsageThresholds) > 0 {
		p.ProdUsageThresholds = vectorizer.ToFactorVec(args.ProdUsageThresholds)
	}
	if aggArgs := args.Aggregated; aggArgs != nil && len(aggArgs.UsageThresholds) > 0 && aggArgs.UsageAggregationType != "" {
		p.AggregatedUsage = &aggregatedUsageFilterProfile{
			UsageThresholds:         vectorizer.ToFactorVec(args.Aggregated.UsageThresholds),
			UsageAggregationType:    args.Aggregated.UsageAggregationType,
			UsageAggregatedDuration: args.Aggregated.UsageAggregatedDuration,
		}
	}
	return p
}

// NOTICE: unknown resource name in custom usage thresholds for vectorizer will be skipped in calculation.
//
// Currently, we add all supported resources (cpu, memory) collected by koordlet with hard code
// that can be used in load aware plugin for compatibility.
func (tfp *usageThresholdsFilterProfile) generateUsageThresholdsFilterProfile(node *corev1.Node, vectorizer ResourceVectorizer) *usageThresholdsFilterProfile {
	c, err := extension.GetCustomUsageThresholds(node)
	if err != nil {
		klog.V(5).ErrorS(err, "failed to GetCustomUsageThresholds from", "node", node.Name)
		return tfp
	}
	if c == nil {
		return tfp
	}
	if aggArgs := c.AggregatedUsage; aggArgs != nil && !(len(aggArgs.UsageThresholds) > 0 && aggArgs.UsageAggregationType != "") {
		c.AggregatedUsage = nil
	}
	if len(c.UsageThresholds) == 0 && len(c.ProdUsageThresholds) == 0 && c.AggregatedUsage == nil {
		return tfp
	}
	p := &usageThresholdsFilterProfile{}
	if len(c.UsageThresholds) == 0 {
		p.UsageThresholds = tfp.UsageThresholds
	} else {
		p.UsageThresholds = vectorizer.ToFactorVec(c.UsageThresholds)
	}
	if len(c.ProdUsageThresholds) == 0 {
		p.ProdUsageThresholds = tfp.ProdUsageThresholds
	} else {
		p.ProdUsageThresholds = vectorizer.ToFactorVec(c.ProdUsageThresholds)
	}
	if c.AggregatedUsage == nil {
		p.AggregatedUsage = tfp.AggregatedUsage
	} else {
		p.AggregatedUsage = &aggregatedUsageFilterProfile{
			UsageThresholds:      vectorizer.ToFactorVec(c.AggregatedUsage.UsageThresholds),
			UsageAggregationType: c.AggregatedUsage.UsageAggregationType,
		}
		if d := c.AggregatedUsage.UsageAggregatedDuration; d != nil {
			p.AggregatedUsage.UsageAggregatedDuration = *d
		}
	}
	return p
}

func getResourceValue(resourceName corev1.ResourceName, quantity resource.Quantity) int64 {
	if resourceName == corev1.ResourceCPU {
		return quantity.MilliValue()
	}
	return quantity.Value()
}

func getResourceQuantity(resourceName corev1.ResourceName, value int64) resource.Quantity {
	switch {
	case resourceName == corev1.ResourceCPU:
		return *resource.NewMilliQuantity(value, resource.DecimalSI)
	case resourceName == corev1.ResourceMemory && value%1024 == 0:
		return *resource.NewQuantity(value, resource.BinarySI)
	default:
		return *resource.NewQuantity(value, resource.DecimalSI)
	}
}

// isDaemonSetPod returns true if the pod is a IsDaemonSetPod.
func isDaemonSetPod(ownerRefList []metav1.OwnerReference) bool {
	for _, ownerRef := range ownerRefList {
		if ownerRef.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

type ResourceVectorizer []corev1.ResourceName
type ResourceVector []int64

func NewResourceVectorizer(names ...corev1.ResourceName) ResourceVectorizer {
	sort.Slice(names, func(i, j int) bool {
		return names[i] < names[j]
	})
	return ResourceVectorizer(names)
}

// NOTICE: unknown resource name will be ignored in vectorization
func (rv ResourceVectorizer) ToVec(list corev1.ResourceList) ResourceVector {
	vec := make(ResourceVector, len(rv))
	for i, name := range rv {
		if q, ok := list[name]; ok {
			vec[i] = getResourceValue(name, q)
		}
	}
	return vec
}

// NOTICE: unknown resource name will be ignored in vectorization
func (rv ResourceVectorizer) ToFactorVec(list map[corev1.ResourceName]int64) ResourceVector {
	vec := make(ResourceVector, len(rv))
	for i, name := range rv {
		vec[i] = list[name]
	}
	return vec
}

func (rv ResourceVectorizer) ToList(vec ResourceVector) corev1.ResourceList {
	list := make(corev1.ResourceList, len(rv))
	for i, name := range rv {
		list[name] = getResourceQuantity(name, vec[i])
	}
	return list
}

func (rv ResourceVectorizer) ToFactorList(vec ResourceVector) map[corev1.ResourceName]int64 {
	list := make(map[corev1.ResourceName]int64, len(rv))
	for i, name := range rv {
		list[name] = vec[i]
	}
	return list
}

func (rv ResourceVectorizer) EmptyVec() ResourceVector {
	return make(ResourceVector, len(rv))
}

func (v ResourceVector) Empty() bool {
	for i := range v {
		if v[i] != 0 {
			return false
		}
	}
	return true
}

func (v ResourceVector) Add(y ResourceVector) {
	for i := range v {
		v[i] += y[i]
	}
}

// v = v + max(0, x - y)
func (v ResourceVector) AddDelta(x, y ResourceVector) (changed bool) {
	for i, value := range x {
		if y != nil {
			value -= y[i]
		}
		if value > 0 {
			v[i] += value
			changed = true
		}
	}
	return
}

func (v ResourceVector) Sub(y ResourceVector) {
	for i := range v {
		v[i] -= y[i]
	}
}

// v = v - max(0, x - y)
func (v ResourceVector) SubDelta(x, y ResourceVector) (changed bool) {
	for i, value := range x {
		if y != nil {
			value -= y[i]
		}
		if value > 0 {
			v[i] -= value
			changed = true
		}
	}
	return
}

func (v ResourceVector) Clone() framework.StateData {
	if v == nil {
		return nil
	}
	copy := make(ResourceVector, len(v))
	for i := range v {
		copy[i] = v[i]
	}
	return copy
}
