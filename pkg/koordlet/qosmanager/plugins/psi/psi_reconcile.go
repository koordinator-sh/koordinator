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

package psi

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/cgroup"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/operator"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

const (
	PSIReconcileName = "PSIReconcile"
)

var _ framework.QOSStrategy = &psiReconcile{}

type psiReconcile struct {
	reconcileInterval time.Duration
	statesInformer    statesinformer.StatesInformer
	manager           *PsiManager
}

func (p *psiReconcile) Enabled() bool {
	return features.DefaultKoordletFeatureGate.Enabled(features.BlkIOReconcile) && p.reconcileInterval > 0
}

func (p *psiReconcile) Setup(context *framework.Context) {
}

func (p *psiReconcile) Run(stopCh <-chan struct{}) {
	go wait.Until(p.reconcile, p.reconcileInterval, stopCh)
}

type (
	GetUpdaterFunc      func(block *slov1alpha1.BlockCfg, diskNumber string, dynamicPath string) (resources []resourceexecutor.ResourceUpdater)
	GetRemoverFunc      func(diskNumber string, dynamicPath string) (resources []resourceexecutor.ResourceUpdater)
	GetDiskRecorderFunc func(absolutePath string) (map[string]bool, error)
)

func New(opt *framework.Options) framework.QOSStrategy {
	p := &psiReconcile{
		reconcileInterval: time.Duration(opt.Config.ReconcileIntervalSeconds) * time.Second,
		statesInformer:    opt.StatesInformer,
		manager:           NewManager(cgroup.ResourceMaskAll),
	}
	opt.StatesInformer.RegisterCallbacks(statesinformer.RegisterTypeAllPods, "psi-reconcile",
		"Update psi managed pod", p.updatePods)
	return p
}

func (p *psiReconcile) updatePods(t statesinformer.RegisterType, o interface{}, target *statesinformer.CallbackTarget) {
	if err := p.manager.Batch(target.Pods); err != nil {
		klog.ErrorS(err, "Failed to update pods")
	}
}

func (p *psiReconcile) reconcile() {
	start := time.Now()
	defer func() {
		if time.Since(start) > p.reconcileInterval {
			klog.Warningf("PSI reconcile takes too much time: %v > %v", time.Since(start), p.reconcileInterval)
		}
	}()

	node := p.statesInformer.GetNode()
	if node == nil || node.Status.Allocatable == nil {
		klog.ErrorS(fmt.Errorf("node is invalid"), "Failed to reconcile PSI")
		return
	}
	nodeSLO := p.statesInformer.GetNodeSLO()
	if nodeSLO == nil || nodeSLO.Spec.PSIStrategy == nil {
		klog.ErrorS(fmt.Errorf("nodeSLO is invalid"), "Failed to reconcile PSI")
		return
	}
	ops, err := extractOperators(nodeSLO.Spec.PSIStrategy)
	if err != nil {
		klog.ErrorS(fmt.Errorf("failed to extract operators: %v", err), "Failed to reconcile PSI")
		return
	}
	if err := p.manager.AddOperators(ops...); err != nil {
		klog.ErrorS(fmt.Errorf("failed to add operators: %v", err), "Failed to reconcile PSI")
		return
	}
	if err := p.manager.Reconcile(node); err != nil {
		klog.ErrorS(err, "Failed to reconcile PSI")
	}
}

func extractOperators(strategy *slov1alpha1.PSIStrategy) (res []operator.Operator, err error) {
	if strategy.PSIExport != nil && strategy.PSIExport.Enable != nil && *strategy.PSIExport.Enable {
		op := &operator.PSIExport{}
		if err := op.Update(operator.DefaultPSIExporter); err != nil {
			return nil, err
		}
		if strategy.PSIExport.Threshold.CPU != nil {
			op.CPU = &operator.StallThreshold{
				Avg10:  float64(strategy.PSIExport.Threshold.CPU.Avg10) / 100,
				Avg60:  float64(strategy.PSIExport.Threshold.CPU.Avg60) / 100,
				Avg300: float64(strategy.PSIExport.Threshold.CPU.Avg300) / 100,
			}
		}
		if strategy.PSIExport.Threshold.Memory != nil {
			op.Memory = &operator.StallThreshold{
				Avg10:  float64(strategy.PSIExport.Threshold.Memory.Avg10) / 100,
				Avg60:  float64(strategy.PSIExport.Threshold.Memory.Avg60) / 100,
				Avg300: float64(strategy.PSIExport.Threshold.Memory.Avg300) / 100,
			}
		}
		if strategy.PSIExport.Threshold.IO != nil {
			op.IO = &operator.StallThreshold{
				Avg10:  float64(strategy.PSIExport.Threshold.IO.Avg10) / 100,
				Avg60:  float64(strategy.PSIExport.Threshold.IO.Avg60) / 100,
				Avg300: float64(strategy.PSIExport.Threshold.IO.Avg300) / 100,
			}
		}
		res = append(res, op)
	}
	if strategy.MemorySuppress != nil && strategy.MemorySuppress.Enable != nil && *strategy.MemorySuppress.Enable {
		op := &operator.MemorySuppress{}
		if err := op.Update(operator.DefaultMemorySuppress); err != nil {
			return nil, err
		}
		if strategy.MemorySuppress.MinSpot != nil {
			op.MinSpot = float64(*strategy.MemorySuppress.MinSpot) / 10000
		}
		if strategy.MemorySuppress.MaxSpot != nil {
			op.MaxSpot = float64(*strategy.MemorySuppress.MaxSpot) / 10000
		}
		if strategy.MemorySuppress.GrowPeriods != nil {
			op.GrowPeriods = *strategy.MemorySuppress.GrowPeriods
		}
		if strategy.MemorySuppress.KillPeriods != nil {
			op.KillPeriods = *strategy.MemorySuppress.KillPeriods
		}
		res = append(res, op)
	}
	if strategy.BudgetBalance != nil && strategy.BudgetBalance.Enable != nil && *strategy.BudgetBalance.Enable {
		op := &operator.BudgetBalance{}
		// TODO: support other resource
		if err := op.Update(operator.DefaultCpuBudgetBalance); err != nil {
			return nil, err
		}
		if strategy.BudgetBalance.BasePrice != nil {
			op.BasePrice = float64(*strategy.BudgetBalance.BasePrice) / 100
		}
		if strategy.BudgetBalance.LowerBound != nil {
			op.LowerBound = float64(*strategy.BudgetBalance.LowerBound) / 10000
		}
		res = append(res, op)
	}
	if strategy.GroupShare != nil && strategy.GroupShare.Enable != nil && *strategy.GroupShare.Enable {
		op := &operator.GroupShare{}
		// TODO: support other resource
		if err := op.Update(operator.DefaultCpuGroupShare); err != nil {
			return nil, err
		}
		if strategy.GroupShare.GroupingAnnotationKey != nil {
			op.GroupingAnnotationKey = *strategy.GroupShare.GroupingAnnotationKey
		}
		if strategy.GroupShare.LowerBound != nil {
			op.LowerBound = float64(*strategy.GroupShare.LowerBound) / 10000
		}
		res = append(res, op)
	}
	return res, nil
}
