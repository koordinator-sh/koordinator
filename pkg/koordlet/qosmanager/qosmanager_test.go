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
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"

	clientsetalpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	maframework "github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
)

func TestNewResManager(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		scheme := apiruntime.NewScheme()
		kubeClient := &kubernetes.Clientset{}
		crdClient := &clientsetalpha1.Clientset{}
		nodeName := "test-node"
		statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
		metricCache := mock_metriccache.NewMockMetricCache(ctrl)
		cfg := framework.NewDefaultConfig()
		cfg.EvictByCopilotAgent = true
		r := NewQOSManager(cfg, scheme, kubeClient, crdClient, nodeName, statesInformer, metricCache, maframework.NewDefaultConfig(), policyv1beta1.SchemeGroupVersion.String())
		assert.NotNil(t, r)
	})
}
