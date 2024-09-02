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

package options

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	quotav1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

var Scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = configv1alpha1.AddToScheme(clientgoscheme.Scheme)
	_ = quotav1alpha1.AddToScheme(clientgoscheme.Scheme)
	_ = slov1alpha1.AddToScheme(clientgoscheme.Scheme)
	_ = schedulingv1alpha1.AddToScheme(clientgoscheme.Scheme)

	_ = configv1alpha1.AddToScheme(Scheme)
	_ = quotav1alpha1.AddToScheme(Scheme)
	_ = slov1alpha1.AddToScheme(Scheme)
	_ = schedulingv1alpha1.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)

	Scheme.AddUnversionedTypes(metav1.SchemeGroupVersion, &metav1.UpdateOptions{}, &metav1.DeleteOptions{}, &metav1.CreateOptions{})
	// +kubebuilder:scaffold:scheme
}
