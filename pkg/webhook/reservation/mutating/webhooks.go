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

// +kubebuilder:webhook:path=/mutate-reservation,mutating=true,failurePolicy=fail,sideEffects=None,admissionReviewVersions=v1;v1beta1,groups="scheduling.koordinator.sh",resources=reservations,verbs=create,versions=v1alpha1,name=mreservation-create.koordinator.sh

var (
	// HandlerBuilderMap contains admission webhook handlers builder
	HandlerBuilderMap = map[string]framework.HandlerBuilder{
		"mutate-reservation": &reservationMutateBuilder{},
	}
)

var _ framework.HandlerBuilder = &reservationMutateBuilder{}

type reservationMutateBuilder struct {
	mgr manager.Manager
}

func (b *reservationMutateBuilder) WithControllerManager(mgr ctrl.Manager) framework.HandlerBuilder {
	b.mgr = mgr
	return b
}

func (b *reservationMutateBuilder) Build() admission.Handler {
	return &ReservationMutatingHandler{
		Client:  b.mgr.GetClient(),
		Decoder: admission.NewDecoder(b.mgr.GetScheme()),
	}
}
