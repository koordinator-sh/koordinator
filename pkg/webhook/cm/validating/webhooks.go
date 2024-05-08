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

package validating

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/webhook/util/framework"
)

// +kubebuilder:webhook:path=/validate-configmap,mutating=false,failurePolicy=fail,sideEffects=None,groups="",resources=configmaps,verbs=create;update;delete,versions=v1,name=vconfigmap.koordinator.sh,admissionReviewVersions={v1beta1}

var (
	// HandlerBuilderMap HandlerMap contains admission webhook handlers builder
	HandlerBuilderMap = map[string]framework.HandlerBuilder{
		"validate-configmap": &cmBuilder{},
	}
)

var _ framework.HandlerBuilder = &cmBuilder{}

type cmBuilder struct {
	mgr manager.Manager
}

func (b *cmBuilder) WithControllerManager(mgr ctrl.Manager) framework.HandlerBuilder {
	b.mgr = mgr
	return b
}

func (b *cmBuilder) Build() admission.Handler {
	return NewConfigMapValidatingHandler(b.mgr.GetClient(), admission.NewDecoder(b.mgr.GetScheme()))
}
