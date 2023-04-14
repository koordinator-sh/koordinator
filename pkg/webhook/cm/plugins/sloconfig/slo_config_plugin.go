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

package sloconfig

import (
	"context"
	"fmt"
	"sync"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrladmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/util/parallelize"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

const (
	PluginName = "SLOConfig"
)

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

type SLOControllerPlugin struct {
	client  ctrlclient.Client
	decoder *ctrladmission.Decoder
}

func NewPlugin(decoder *ctrladmission.Decoder, client ctrlclient.Client) *SLOControllerPlugin {
	return &SLOControllerPlugin{client: client, decoder: decoder}
}

func (cl *SLOControllerPlugin) Name() string {
	return PluginName
}

func (cl *SLOControllerPlugin) Admit(ctx context.Context, req ctrladmission.Request, config, oldConfig *corev1.ConfigMap) error {
	return nil
}

func (cl *SLOControllerPlugin) Validate(ctx context.Context, req ctrladmission.Request, config, oldConfig *corev1.ConfigMap) error {
	if config.Namespace != sloconfig.ConfigNameSpace || config.Name != sloconfig.SLOCtrlConfigMap {
		return nil
	}
	klog.Infof("enter SLOControllerConfig plugin validate for %s/%s", req.AdmissionRequest.Namespace, req.AdmissionRequest.Name)

	op := req.AdmissionRequest.Operation
	switch op {
	case admissionv1.Create:
		return cl.checkConfig(ctx, nil, config)
	case admissionv1.Update:
		return cl.checkConfig(ctx, oldConfig, config)
	case admissionv1.Delete:
		klog.Warningf("slo-manager-config cm delete by user : %s", req.UserInfo.Username)
		return nil
	}
	return nil
}

func (cl *SLOControllerPlugin) checkConfig(ctx context.Context, oldConfig *corev1.ConfigMap, config *corev1.ConfigMap) error {
	checkers := CreateCheckersChanged(oldConfig, config)

	err := checkers.CheckConfigContents()
	if err != nil {
		return err
	}

	if !checkers.NeedCheckForNodes() {
		return nil
	}

	nodeList := &corev1.NodeList{}
	if err := cl.client.List(context.TODO(), nodeList); err != nil {
		return fmt.Errorf("check config conflict fail! list node fail,error:%v", err)
	}

	if len(nodeList.Items) == 0 {
		return nil
	}
	klog.V(3).Infof("start check nodes,num:%d", len(nodeList.Items))
	return parallelizeCheckNode(ctx, nodeList.Items, checkers)
}

func parallelizeCheckNode(ctx context.Context, nodeList []corev1.Node, checkers checkers) error {
	var lock sync.Mutex
	var err error

	context, cancel := context.WithCancel(ctx)

	checkNode := func(i int) {
		for _, checker := range checkers {
			checkErr := checker.ExistNodeConflict(&nodeList[i])
			if checkErr != nil {
				lock.Lock()
				err = checkErr
				lock.Unlock()
				cancel()
				return
			}
		}
	}

	parallelize.Until(context, len(nodeList), checkNode)

	return err
}
