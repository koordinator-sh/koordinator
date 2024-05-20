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

package mutating

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/webhook/util/framework"
)

// +kubebuilder:webhook:path=/mutate-node-status,mutating=true,failurePolicy=ignore,sideEffects=None,groups="",resources=nodes/status,verbs=create;update,versions=v1,name=mnode-status.koordinator.sh,admissionReviewVersions=v1;v1beta1

var (
	// HandlerBuilderMap contains admission webhook handlers builder
	HandlerBuilderMap = map[string]framework.HandlerBuilder{
		"mutate-node-status": &nodeMutateBuilder{},
	}
)

var _ framework.HandlerBuilder = &nodeMutateBuilder{}

type nodeMutateBuilder struct {
	mgr manager.Manager
}

func (b *nodeMutateBuilder) WithControllerManager(mgr ctrl.Manager) framework.HandlerBuilder {
	b.mgr = mgr
	return b
}

func (b *nodeMutateBuilder) Build() admission.Handler {
	return NewNodeStatusMutatingHandler(b.mgr.GetClient(), admission.NewDecoder(b.mgr.GetScheme()))
}
