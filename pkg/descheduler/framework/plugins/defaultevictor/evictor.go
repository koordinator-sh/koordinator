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

package defaultevictor

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	v1 "k8s.io/client-go/listers/core/v1"
	schedulingv1 "k8s.io/client-go/listers/scheduling/v1"
	core "k8s.io/client-go/testing"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/apis/policy"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/evictions"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/utils"
)

const (
	PluginName = "DefaultEvictor"
)

type DefaultEvictor struct {
	handle        framework.Handle
	evictorFilter *evictions.EvictorFilter
	evictor       *evictions.PodEvictor
}

var _ framework.Evictor = &DefaultEvictor{}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	evictorArgs, ok := args.(*deschedulerconfig.DefaultEvictorArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type DefaultEvictorArgs, got %T", args)
	}

	nodesGetter := func() ([]*corev1.Node, error) {
		nodesLister := handle.SharedInformerFactory().Core().V1().Nodes().Lister()
		return nodesLister.List(labels.Everything())
	}

	var selector labels.Selector
	if evictorArgs.LabelSelector != nil {
		var err error
		selector, err = metav1.LabelSelectorAsSelector(evictorArgs.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to get label selectors: %v", err)
		}
	}

	podLister := handle.SharedInformerFactory().Core().V1().Pods().Lister()
	nodeLister := handle.SharedInformerFactory().Core().V1().Nodes().Lister()
	namespaceLister := handle.SharedInformerFactory().Core().V1().Namespaces().Lister()
	priorityClassLister := handle.SharedInformerFactory().Scheduling().V1().PriorityClasses().Lister()
	priorityThreshold, err := utils.GetPriorityValueFromPriorityThreshold(priorityClassLister, evictorArgs.PriorityThreshold)
	if err != nil {
		return nil, err
	}

	evictorFilter := evictions.NewEvictorFilter(
		nodesGetter,
		handle.GetPodsAssignedToNodeFunc(),
		evictorArgs.EvictLocalStoragePods,
		evictorArgs.EvictSystemCriticalPods,
		evictorArgs.IgnorePvcPods,
		evictorArgs.EvictFailedBarePods,
		evictions.WithNodeFit(evictorArgs.NodeFit),
		evictions.WithLabelSelector(selector),
		evictions.WithPriorityThreshold(priorityThreshold),
	)

	var podEvictorClient clientset.Interface
	if evictorArgs.DryRun {
		klog.V(3).Infof("Building a fake client from the cluster for the dry run")
		fakeClientSet, err := fakeClient(podLister, nodeLister, namespaceLister, priorityClassLister)
		if err != nil {
			klog.Error(err)
			return nil, err
		}

		podEvictorClient = fakeClientSet
	} else {
		podEvictorClient = handle.ClientSet()
	}

	podEvictor := evictions.NewPodEvictor(
		podEvictorClient,
		handle.EventRecorder(),
		"",
		evictorArgs.DryRun,
		evictorArgs.MaxNoOfPodsToEvictPerNode,
		evictorArgs.MaxNoOfPodsToEvictPerNamespace,
	)

	return &DefaultEvictor{
		handle:        handle,
		evictorFilter: evictorFilter,
		evictor:       podEvictor,
	}, nil
}

func (d *DefaultEvictor) Name() string {
	return PluginName
}

func (d *DefaultEvictor) Filter(pod *corev1.Pod) bool {
	return d.evictorFilter.Filter(pod)
}

func (d *DefaultEvictor) Evict(ctx context.Context, pod *corev1.Pod, evictOptions framework.EvictOptions) bool {
	return d.evictor.Evict(ctx, pod, evictOptions)
}

func fakeClient(podLister v1.PodLister, nodeLister v1.NodeLister, namespaceLister v1.NamespaceLister, priorityClassLister schedulingv1.PriorityClassLister) (clientset.Interface, error) {
	fakeClientSet := fake.NewSimpleClientset()
	fakeClientSet.PrependReactor("create", "pods", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		switch action.GetSubresource() {
		case "eviction":
			act, ok := action.(core.CreateActionImpl)
			if !ok {
				return false, nil, fmt.Errorf("unable to convert action to core.CreateActionImpl")
			}
			evictionObj, ok := act.Object.(*policy.Eviction)
			if !ok {
				return false, nil, fmt.Errorf("unable to convert action object into *policy.Eviction")
			}
			if err := fakeClientSet.Tracker().Delete(act.GetResource(), evictionObj.GetNamespace(), evictionObj.GetName()); err != nil {
				return false, nil, fmt.Errorf("unable to delete pod %v/%v in fake: %v", evictionObj.GetNamespace(), evictionObj.GetName(), err)
			}
			return true, nil, nil
		}
		return false, nil, nil
	})

	klog.V(3).Infof("Pulling resources for the fake client from the cluster")
	pods, err := podLister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("unable to list pods: %v", err)
	}

	for _, pod := range pods {
		if _, err := fakeClientSet.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("unable to copy pod to fake: %v", err)
		}
	}

	nodes, err := nodeLister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("unable to list nodes: %v", err)
	}

	for _, node := range nodes {
		if _, err := fakeClientSet.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("unable to copy node to fake: %v", err)
		}
	}

	namespaces, err := namespaceLister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("unable to list namespaces: %v", err)
	}

	for _, namespace := range namespaces {
		if _, err := fakeClientSet.CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("unable to copy namespace to fake: %v", err)
		}
	}

	priorityClasses, err := priorityClassLister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("unable to list priorityClasses: %v", err)
	}

	for _, priorityClass := range priorityClasses {
		if _, err := fakeClientSet.SchedulingV1().PriorityClasses().Create(context.TODO(), priorityClass, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("unable to copy priorityClass to fake: %v", err)
		}
	}

	return fakeClientSet, nil
}
