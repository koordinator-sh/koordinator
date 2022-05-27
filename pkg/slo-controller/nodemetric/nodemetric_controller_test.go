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

package nodemetric

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource"
)

func newNodeForTest(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func createTestReconciler() *NodeMetricReconciler {
	scheme := runtime.NewScheme()
	slov1alpha1.AddToScheme(scheme)
	clientgoscheme.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	return &NodeMetricReconciler{
		Client: client,
		Scheme: scheme,
	}
}

func Test_CreateNodeMetricWhenNodeNotExist(t *testing.T) {
	reconciler := createTestReconciler()
	nodeName := "test-node"
	node := newNodeForTest(nodeName)
	ctx := context.Background()
	err := reconciler.Create(ctx, node)
	if err != nil {
		t.Fatalf("failed create node: %v", err)
	}

	key := types.NamespacedName{Name: nodeName}
	nodeReq := ctrl.Request{NamespacedName: key}
	reconciler.Reconcile(ctx, nodeReq)

	createdNodeMetric := &slov1alpha1.NodeMetric{}
	err = reconciler.Get(ctx, key, createdNodeMetric)
	if err != nil {
		t.Fatal("nodeMetric should created", err)
	}
}

func Test_RemoveNodeMetricWhenNodeNotExist(t *testing.T) {
	reconciler := createTestReconciler()
	nodeName := "test-node"

	nodeMetric := &slov1alpha1.NodeMetric{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
	ctx := context.Background()
	err := reconciler.Create(ctx, nodeMetric)
	if err != nil {
		t.Fatalf("failed create nodemetric: %v", err)
	}

	key := types.NamespacedName{Name: nodeName}
	nodeReq := ctrl.Request{NamespacedName: key}
	reconciler.Reconcile(ctx, nodeReq)

	createdNodeMetric := &slov1alpha1.NodeMetric{}
	err = reconciler.Get(ctx, key, createdNodeMetric)
	if !errors.IsNotFound(err) {
		t.Fatal("nodeMetric should be removed", err)
	}
}

func Test_UpdateNodeMetricFromConfigmap(t *testing.T) {
	reconciler := createTestReconciler()
	nodeName := "test-node"

	nodeMetric := &slov1alpha1.NodeMetric{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
	ctx := context.Background()
	err := reconciler.Create(ctx, nodeMetric)
	if err != nil {
		t.Fatalf("failed create nodemetric: %v", err)
	}
	err = reconciler.Create(ctx, newNodeForTest(nodeName))
	if err != nil {
		t.Fatalf("failed create node: %v", err)
	}

	policyConfig := &config.ColocationCfg{
		ColocationStrategy: config.ColocationStrategy{
			Enable:                         pointer.Bool(true),
			MetricAggregateDurationSeconds: pointer.Int64(60),
			MetricReportIntervalSeconds:    pointer.Int64(180),
		},
	}
	data, err := json.Marshal(policyConfig)
	if err != nil {
		t.Fatal("failed to marshal ColocationCfg", err)
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: config.ConfigNameSpace,
			Name:      config.SLOCtrlConfigMap,
		},
		Data: map[string]string{
			config.ColocationConfigKey: string(data),
		},
	}
	err = reconciler.Create(ctx, configMap)
	if err != nil {
		t.Fatalf("failed create configmap: %v", err)
	}

	h := &noderesource.EnqueueRequestForConfigMap{Client: reconciler.Client, Config: &reconciler.config}
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	h.Create(event.CreateEvent{Object: configMap}, queue)

	key := types.NamespacedName{Name: nodeName}
	nodeReq := ctrl.Request{NamespacedName: key}
	reconciler.Reconcile(ctx, nodeReq)

	nodeMetric = &slov1alpha1.NodeMetric{}
	err = reconciler.Get(ctx, key, nodeMetric)
	if err != nil {
		t.Fatal("get nodeMetric failed", err)
	}
	if *nodeMetric.Spec.CollectPolicy.AggregateDurationSeconds != *policyConfig.ColocationStrategy.MetricAggregateDurationSeconds {
		t.Errorf("unexpected AggregateDurationSeconds, get %v expected %v", *nodeMetric.Spec.CollectPolicy.AggregateDurationSeconds, *policyConfig.ColocationStrategy.MetricAggregateDurationSeconds)
	}

	if *nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds != *policyConfig.ColocationStrategy.MetricReportIntervalSeconds {
		t.Errorf("unexpected ReportIntervalSeconds, get %v expected %v", *nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds, *policyConfig.ColocationStrategy.MetricReportIntervalSeconds)
	}
}
