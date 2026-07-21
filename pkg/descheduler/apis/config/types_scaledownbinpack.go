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

package config

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ScaleDownBinPackArgs holds arguments used to configure the ScaleDownBinPack plugin.
type ScaleDownBinPackArgs struct {
	metav1.TypeMeta

	// Paused indicates whether the ScaleDownBinPack should to work or not.
	// Default is false.
	Paused bool

	// Strategy indicates the node evacuation strategy.
	// Default is CalculateOnly.
	Strategy ScaleDownBinPackStrategy

	// MaxPodsToEvict is the maximum number of pods to evict when Strategy is EvictDirectly.
	MaxPodsToEvict *int32

	// NodeSelector selects the nodes that matched labelSelector.
	NodeSelector *metav1.LabelSelector

	// PodSelectors selects the pods that matched labelSelector.
	PodSelectors []ScaleDownBinPackPodSelector

	// EvictableNamespaces carries a list of included/excluded namespaces
	// for which pods are evictable.
	EvictableNamespaces *Namespaces

	// Resources to be considered in the binpack strategy.
	// Default is [cpu, memory].
	Resources []corev1.ResourceName

	// ResourceWeights indicates the weights of resources.
	// The weights of resources are 1.0 by default.
	ResourceWeights map[corev1.ResourceName]float64
}

// ScaleDownBinPackStrategy is a string enum for the scale-down binpack strategy.
type ScaleDownBinPackStrategy string

const (
	// ScaleDownBinPackStrategyCalculateOnly calculates and sets pod deletion cost.
	ScaleDownBinPackStrategyCalculateOnly ScaleDownBinPackStrategy = "CalculateOnly"
	// ScaleDownBinPackStrategyEvictDirectly greedily evicts target pods up to MaxPodsToEvict.
	ScaleDownBinPackStrategyEvictDirectly ScaleDownBinPackStrategy = "EvictDirectly"
)

// ScaleDownBinPackPodSelector contains the Name and Selector to match pods.
type ScaleDownBinPackPodSelector struct {
	// Name represents the name of selector.
	Name string
	// Selector label query over pods for migrated.
	Selector *metav1.LabelSelector
}
