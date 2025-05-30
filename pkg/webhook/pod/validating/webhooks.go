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
	"context"

	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	extclient "github.com/koordinator-sh/koordinator/pkg/client"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	"github.com/koordinator-sh/koordinator/pkg/webhook/quotaevaluate"
	"github.com/koordinator-sh/koordinator/pkg/webhook/util/framework"
)

// +kubebuilder:webhook:path=/validate-pod,mutating=false,failurePolicy=fail,sideEffects=None,admissionReviewVersions=v1;v1beta1,groups="",resources=pods,verbs=create;update,versions=v1,name=vpod.koordinator.sh

var (
	// HandlerMap contains admission webhook handlers
	HandlerBuilderMap = map[string]framework.HandlerBuilder{
		"validate-pod": &podValidateBuilder{},
	}
)

var _ framework.HandlerBuilder = &podValidateBuilder{}

type podValidateBuilder struct {
	mgr manager.Manager
}

func (b *podValidateBuilder) WithControllerManager(mgr ctrl.Manager) framework.HandlerBuilder {
	b.mgr = mgr
	return b
}

func (b *podValidateBuilder) Build() admission.Handler {
	h := &PodValidatingHandler{
		Client:  b.mgr.GetClient(),
		Decoder: admission.NewDecoder(b.mgr.GetScheme()),
	}
	quotaAccessor := quotaevaluate.NewQuotaAccessor(h.Client)
	h.QuotaEvaluator = quotaevaluate.NewQuotaEvaluator(quotaAccessor, 16, make(chan struct{}))

	if utilfeature.DefaultFeatureGate.Enabled(features.EnablePodEnhancedValidator) {
		kubeClient := extclient.GetGenericClientWithName(ClientNamePodEnhancedValidator).KubeClient
		h.PodEnhancedValidator = NewPodEnhancedValidator(kubeClient)
		if err := h.PodEnhancedValidator.Start(context.Background()); err != nil {
			klog.Fatalf("failed to start pod-enhanced-validator, err=%v", err)
		}
	}

	return h
}
