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

package noderesource

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type nodeBEResource struct {
	IsColocationAvailable bool

	MilliCPU *resource.Quantity
	Memory   *resource.Quantity

	Reason  string
	Message string
}

type SyncContext struct {
	lock       sync.RWMutex
	contextMap map[string]time.Time
}

func NewSyncContext() SyncContext {
	return SyncContext{
		contextMap: map[string]time.Time{},
	}
}

func (s *SyncContext) Load(key string) (time.Time, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	value, ok := s.contextMap[key]
	return value, ok
}

func (s *SyncContext) Store(key string, value time.Time) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.contextMap[key] = value
}

func (s *SyncContext) Delete(key string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.contextMap, key)
}

func (r *NodeResourceReconciler) isColocationCfgDisabled(node *corev1.Node) bool {
	cfg := r.cfgCache.GetCfgCopy()
	if cfg.Enable == nil || !*cfg.Enable {
		return true
	}
	strategy := config.GetNodeColocationStrategy(cfg, node)
	if strategy == nil || strategy.Enable == nil {
		return true
	}
	return !(*strategy.Enable)
}

func (r *NodeResourceReconciler) isDegradeNeeded(nodeMetric *slov1alpha1.NodeMetric, node *corev1.Node) bool {
	if nodeMetric == nil || nodeMetric.Status.UpdateTime == nil {
		klog.Warningf("invalid NodeMetric: %v, need degradation", nodeMetric)
		return true
	}

	strategy := config.GetNodeColocationStrategy(r.cfgCache.GetCfgCopy(), node)

	if r.Clock.Now().After(nodeMetric.Status.UpdateTime.Add(time.Duration(*strategy.DegradeTimeMinutes) * time.Minute)) {
		klog.Warningf("timeout NodeMetric: %v, current timestamp: %v, metric last update timestamp: %v",
			nodeMetric.Name, r.Clock.Now(), nodeMetric.Status.UpdateTime)
		return true
	}

	return false
}

func (r *NodeResourceReconciler) resetNodeBEResource(node *corev1.Node, reason, message string) error {
	beResource := &nodeBEResource{
		IsColocationAvailable: false,
		MilliCPU:              nil,
		Memory:                nil,
		Reason:                reason,
		Message:               message,
	}
	return r.updateNodeBEResource(node, beResource)
}

func (r *NodeResourceReconciler) updateNodeBEResource(node *corev1.Node, beResource *nodeBEResource) error {
	copyNode := node.DeepCopy()

	if err := r.prepareNodeResource(copyNode, beResource); err != nil {
		return err
	}

	if needSync := r.isBEResourceSyncNeeded(node, copyNode); !needSync {
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		updateNode := &corev1.Node{}
		if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: node.Name}, updateNode); err != nil {
			klog.Errorf("failed to get node %v, error: %v", node.Name, err)
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if err := r.prepareNodeResource(updateNode, beResource); err != nil {
			return err
		}

		if err := r.Client.Status().Update(context.TODO(), updateNode); err == nil {
			r.SyncContext.Store(util.GetNodeKey(node), r.Clock.Now())
			klog.V(5).Infof("update node %v success, detail %+v", updateNode.Name, updateNode)
			return nil
		} else {
			klog.Errorf("failed to update node %v, error: %v", updateNode.Name, err)
			return err
		}
	})
}

func (r *NodeResourceReconciler) isBEResourceSyncNeeded(old, new *corev1.Node) bool {
	if new == nil || new.Status.Allocatable == nil || new.Status.Capacity == nil {
		klog.Errorf("invalid input, node should not be nil")
		return false
	}

	strategy := config.GetNodeColocationStrategy(r.cfgCache.GetCfgCopy(), new)

	// scenario 1: update time gap is bigger than UpdateTimeThresholdSeconds
	lastUpdatedTime, ok := r.SyncContext.Load(util.GetNodeKey(new))
	if !ok || r.Clock.Now().After(lastUpdatedTime.Add(time.Duration(*strategy.UpdateTimeThresholdSeconds)*time.Second)) {
		klog.Warningf("node %v resource expired, need sync", new.Name)
		return true
	}

	// scenario 2: resource diff is bigger than ResourceDiffThreshold
	if util.IsResourceDiff(old.Status.Allocatable, new.Status.Allocatable, extension.BatchCPU, *strategy.ResourceDiffThreshold) {
		klog.Warningf("node %v resource diff bigger than %v, need sync", extension.BatchCPU, *strategy.ResourceDiffThreshold)
		return true
	}
	if util.IsResourceDiff(old.Status.Allocatable, new.Status.Allocatable, extension.BatchMemory, *strategy.ResourceDiffThreshold) {
		klog.Warningf("node %v resource diff bigger than %v, need sync", extension.BatchMemory, *strategy.ResourceDiffThreshold)
		return true
	}

	// scenario 3: all good, do nothing
	klog.Info("all good, no need to sync for node %v", new.Name)
	return false
}

func (r *NodeResourceReconciler) prepareNodeResource(node *corev1.Node, beResource *nodeBEResource) error {
	if beResource.MilliCPU == nil {
		delete(node.Status.Capacity, extension.BatchCPU)
		delete(node.Status.Allocatable, extension.BatchCPU)
	} else {
		if _, ok := beResource.MilliCPU.AsInt64(); !ok {
			klog.V(2).Infof("invalid cpu value, cpu quantity %v is not int64", beResource.MilliCPU)
			return fmt.Errorf("invalid cpu value, cpu quantity %v is not int64", beResource.MilliCPU)
		}
		node.Status.Capacity[extension.BatchCPU] = *beResource.MilliCPU
		node.Status.Allocatable[extension.BatchCPU] = *beResource.MilliCPU
	}

	if beResource.Memory == nil {
		delete(node.Status.Capacity, extension.BatchMemory)
		delete(node.Status.Allocatable, extension.BatchMemory)
	} else {
		if _, ok := beResource.Memory.AsInt64(); !ok {
			klog.V(2).Infof("invalid memory value, memory quantity %v is not int64", beResource.Memory)
			return fmt.Errorf("invalid memory value, memory quantity %v is not int64", beResource.Memory)
		}
		node.Status.Capacity[extension.BatchMemory] = *beResource.Memory
		node.Status.Allocatable[extension.BatchMemory] = *beResource.Memory
	}

	strategy := config.GetNodeColocationStrategy(r.cfgCache.GetCfgCopy(), node)
	runNodePrepareExtenders(strategy, node)
	return nil
}
