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
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/webhook/util/framework"
)

// +kubebuilder:webhook:path=/validate-scheduling-sigs-k8s-io-v1alpha1-elasticquota,mutating=false,failurePolicy=fail,sideEffects=None,admissionReviewVersions=v1;v1beta1,groups=scheduling.sigs.k8s.io,resources=elasticquotas,verbs=create;update;delete,versions=v1alpha1,name=velasticquota.koordinator.sh

var (
	// HandlerBuilderMap contains admission webhook handlers builder
	HandlerBuilderMap = map[string]framework.HandlerBuilder{
		"validate-scheduling-sigs-k8s-io-v1alpha1-elasticquota": &quotaValidateBuilder{},
	}
)

var _ framework.HandlerBuilder = &quotaValidateBuilder{}

type quotaValidateBuilder struct {
	mgr manager.Manager
}

func (b *quotaValidateBuilder) WithControllerManager(mgr ctrl.Manager) framework.HandlerBuilder {
	b.mgr = mgr
	return b
}

func (b *quotaValidateBuilder) Build() admission.Handler {
	h := &ElasticQuotaValidatingHandler{
		Client:  b.mgr.GetClient(),
		Decoder: admission.NewDecoder(b.mgr.GetScheme()),
	}
	err := h.InjectCache(b.mgr.GetCache())
	if err != nil {
		klog.Fatal("failed to inject cache for quotaValidateBuilder: %v", err)
	}
	return h
}
