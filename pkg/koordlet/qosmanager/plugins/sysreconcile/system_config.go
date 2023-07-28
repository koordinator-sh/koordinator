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

package sysreconcile

import (
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	SystemConfigReconcileName = "SystemConfigReconcile"
)

type systemConfig struct {
	reconcileInterval time.Duration
	statesInformer    statesinformer.StatesInformer
	executor          resourceexecutor.ResourceUpdateExecutor
}

var _ framework.QOSStrategy = &systemConfig{}

func New(opt *framework.Options) framework.QOSStrategy {
	return &systemConfig{
		reconcileInterval: time.Duration(opt.Config.ReconcileIntervalSeconds) * time.Second,
		statesInformer:    opt.StatesInformer,
		executor:          resourceexecutor.NewResourceUpdateExecutor(),
	}
}

func (r *systemConfig) Enabled() bool {
	return features.DefaultKoordletFeatureGate.Enabled(features.SystemConfig) && r.reconcileInterval > 0
}

func (r *systemConfig) Setup(context *framework.Context) {
}

func (r *systemConfig) Run(stopCh <-chan struct{}) {
	r.init(stopCh)
	go wait.Until(r.reconcile, r.reconcileInterval, stopCh)
}

func (s *systemConfig) init(stopCh <-chan struct{}) {
	s.executor.Run(stopCh)
}

func (s *systemConfig) reconcile() {
	nodeSLO := s.statesInformer.GetNodeSLO()

	if nodeSLO == nil || nodeSLO.Spec.SystemStrategy == nil {
		klog.Warningf("nodeSLO or systemStrategy is nil, skip reconcile systemConfig!")
		return
	}

	node := s.statesInformer.GetNode()
	if node == nil {
		klog.Warningf("systemStrategy config failed, got nil node")
		return
	}
	memoryCapacity := node.Status.Capacity.Memory().Value()
	if memoryCapacity <= 0 {
		klog.Warningf("systemStrategy config failed, node memoryCapacity not valid,value: %d", memoryCapacity)
		return
	}

	var resources []resourceexecutor.ResourceUpdater
	resources = append(resources, caculateMemoryConfig(nodeSLO.Spec.SystemStrategy, memoryCapacity)...)

	s.executor.UpdateBatch(true, resources...)
	klog.V(5).Infof("finish to reconcile system config!")
}

func caculateMemoryConfig(strategy *slov1alpha1.SystemStrategy, nodeMemory int64) []resourceexecutor.ResourceUpdater {
	var resources []resourceexecutor.ResourceUpdater
	if strategy.MinFreeKbytesFactor != nil {
		totalMemory := nodeMemory / 1024 //to kbytes
		if totalMemory <= 0 {
			klog.Errorf("can not change min_free_kbytes! totalMemory invalid ,total memory = %v", totalMemory)
			return resources
		}
		minFreeKbytes := totalMemory * *strategy.MinFreeKbytesFactor / 10000
		if sysutil.ValidateResourceValue(&minFreeKbytes, "", sysutil.MinFreeKbytes) {
			valueStr := strconv.FormatInt(minFreeKbytes, 10)
			file := sysutil.MinFreeKbytes.Path("")
			eventHelper := audit.V(3).Node().Reason("systemConfig reconcile").Message("update calculated mem config min_free_kbytes to : %v", valueStr)
			resource, err := resourceexecutor.NewCommonDefaultUpdater(file, file, valueStr, eventHelper)
			if err != nil {
				return resources
			}
			resources = append(resources, resource)
		}
	}

	if sysutil.ValidateResourceValue(strategy.WatermarkScaleFactor, "", sysutil.WatermarkScaleFactor) {
		valueStr := strconv.FormatInt(*strategy.WatermarkScaleFactor, 10)
		file := sysutil.WatermarkScaleFactor.Path("")
		eventHelper := audit.V(3).Node().Reason("systemConfig reconcile").Message("update calculated mem config watermark_scale_factor to : %v", valueStr)
		resource, err := resourceexecutor.NewCommonDefaultUpdater(file, file, valueStr, eventHelper)
		if err != nil {
			return resources
		}
		resources = append(resources, resource)
	}

	if sysutil.ValidateResourceValue(strategy.MemcgReapBackGround, "", sysutil.MemcgReapBackGround) {
		valueStr := strconv.FormatInt(*strategy.MemcgReapBackGround, 10)
		file := sysutil.MemcgReapBackGround.Path("")
		eventHelper := audit.V(3).Node().Reason("systemConfig reconcile").Message("update calculated mem config reap_background to : %v", valueStr)
		resource, err := resourceexecutor.NewCommonDefaultUpdater(file, file, valueStr, eventHelper)
		if err != nil {
			return resources
		}
		resources = append(resources, resource)
	}
	return resources
}
