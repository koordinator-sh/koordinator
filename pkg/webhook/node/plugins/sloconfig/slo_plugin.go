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
	"reflect"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrladmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
	pluginssloconfig "github.com/koordinator-sh/koordinator/pkg/webhook/cm/plugins/sloconfig"
)

const (
	PluginName = "SLOManagerConfigConflictForNode"
)

type SLOControllerConfigConflict struct {
	client  ctrlclient.Client
	decoder *ctrladmission.Decoder
}

func NewPlugin(decoder *ctrladmission.Decoder, client ctrlclient.Client) *SLOControllerConfigConflict {
	sloConfig := &SLOControllerConfigConflict{client: client, decoder: decoder}
	return sloConfig
}

func (cl *SLOControllerConfigConflict) Name() string {
	return PluginName
}

func (cl *SLOControllerConfigConflict) Admit(ctx context.Context, req ctrladmission.Request, node, oldNode *corev1.Node) error {
	return nil
}

func (cl *SLOControllerConfigConflict) Validate(ctx context.Context, req ctrladmission.Request, node, oldNode *corev1.Node) error {
	klog.Infof("enter SLOControllerConfigConflict plugin validate for %s/%s", req.AdmissionRequest.Namespace, req.AdmissionRequest.Name)

	op := req.AdmissionRequest.Operation
	switch op {
	case admissionv1.Create: // create just check and log
		cl.checkConflict(node, nil)
		return nil
	case admissionv1.Update:
		return cl.checkConflict(node, oldNode)
	}
	return nil
}

func (cl *SLOControllerConfigConflict) checkConflict(node, oldNode *corev1.Node) error {
	if !needCheck(node, oldNode) {
		return nil
	}

	sloCfg := corev1.ConfigMap{}
	err := cl.client.Get(context.Background(), apitypes.NamespacedName{
		Namespace: sloconfig.ConfigNameSpace,
		Name:      sloconfig.SLOCtrlConfigMap,
	}, &sloCfg)

	if err != nil {
		klog.Errorf("failed to get %s config, err: %v", sloconfig.SLOCtrlConfigMap, err)
		return nil
	}
	checkers := pluginssloconfig.CreateCheckersAll(nil, &sloCfg, true)

	for _, checker := range checkers {
		if checker.InitStatus() != pluginssloconfig.InitSuccess {
			continue
		}
		err = checker.ExistNodeConflict(node)
		if err != nil {
			klog.Errorf("config conflict for %s config, err: %s", sloconfig.SLOCtrlConfigMap, err)
			return err
		}
	}

	return nil
}

func needCheck(node, oldNode *corev1.Node) bool {
	if oldNode == nil {
		return true
	}
	return !reflect.DeepEqual(node.Labels, oldNode.Labels)
}
