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

package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"

	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

const (
	// PodMutatingWebhook enables mutating webhook for Pods creations.
	PodMutatingWebhook featuregate.Feature = "PodMutatingWebhook"

	// PodValidatingWebhook enables validating webhook for Pods creations or updates.
	PodValidatingWebhook featuregate.Feature = "PodValidatingWebhook"

	// ElasticQuotaMutatingWebhook enables mutating webhook for ElasticQuotas  creations
	ElasticQuotaMutatingWebhook featuregate.Feature = "ElasticMutatingWebhook"

	// ElasticQuotaValidatingWebhook enables validating webhook for ElasticQuotas creations or updates
	ElasticQuotaValidatingWebhook featuregate.Feature = "ElasticValidatingWebhook"

	// WebhookFramework enables webhook framework
	WebhookFramework featuregate.Feature = "WebhookFramework"
)

var defaultFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	PodMutatingWebhook:            {Default: true, PreRelease: featuregate.Beta},
	PodValidatingWebhook:          {Default: true, PreRelease: featuregate.Beta},
	ElasticQuotaMutatingWebhook:   {Default: true, PreRelease: featuregate.Beta},
	ElasticQuotaValidatingWebhook: {Default: true, PreRelease: featuregate.Beta},
	WebhookFramework:              {Default: true, PreRelease: featuregate.Beta},
}

func init() {
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(defaultFeatureGates))
}

func SetDefaultFeatureGates() {

}
