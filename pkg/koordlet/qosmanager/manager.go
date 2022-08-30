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

package qosmanager

import (
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/k8s"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/metricsquery"
	corev1 "k8s.io/api/core/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/config"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type QoSManager interface {
	Run(stopCh <-chan struct{}) error
}

func NewQosManager(cfg *config.Config, schema *apiruntime.Scheme, kubeClient kubernetes.Interface, nodeName string,
	statesInformer statesinformer.StatesInformer, metricCache metriccache.MetricCache) QoSManager {

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&clientcorev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(schema, corev1.EventSource{Component: "koordlet-qos-manager", Host: nodeName})

	return &qosManager{
		cfg:      cfg,
		nodeName: nodeName,
		pluginCtx: &plugins.PluginContext{
			K8sClient:      k8s.NewK8sClient(kubeClient, recorder),
			StatesInformer: statesInformer,
			MetricCache:    metricCache,
			MetricsQuery:   metricsquery.NewMetricsQuery(metricCache, statesInformer),
		},
	}
}

type qosManager struct {
	cfg       *config.Config
	nodeName  string
	pluginCtx *plugins.PluginContext
}

func (m *qosManager) Run(stopCh <-chan struct{}) error {

}
