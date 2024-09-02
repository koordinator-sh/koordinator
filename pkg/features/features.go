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

	// NodeMutatingWebhook enables mutating webhook for Node Creation or updates
	NodeMutatingWebhook featuregate.Feature = "NodeMutatingWebhook"

	// NodeValidatingWebhook enables validating webhook for Node Creation or updates
	NodeValidatingWebhook featuregate.Feature = "NodeValidatingWebhook"

	// ConfigMapValidatingWebhook enables validating webhook for configmap Creation or updates
	ConfigMapValidatingWebhook featuregate.Feature = "ConfigMapValidatingWebhook"

	// ColocationProfileSkipMutatingResources config whether to update resourceName according to priority by default
	ColocationProfileSkipMutatingResources featuregate.Feature = "ColocationProfileSkipMutatingResources"

	// WebhookFramework enables webhook framework, global feature-gate for webhook
	WebhookFramework featuregate.Feature = "WebhookFramework"

	// MultiQuotaTree enables multi quota tree.
	MultiQuotaTree featuregate.Feature = "MultiQuotaTree"

	// ElasticQuotaIgnorePodOverhead ignore pod.spec.overhead when accounting pod requests
	ElasticQuotaIgnorePodOverhead featuregate.Feature = "ElasticQuotaIgnorePodOverhead"

	// ElasticQuotaIgnoreTerminatingPod ignore the terminating pod.
	ElasticQuotaIgnoreTerminatingPod featuregate.Feature = "ElasticQuotaIgnoreTerminatingPod"

	// ElasticQuotaImmediateIgnoreTerminatingPod ignore the terminating pod immediately.
	ElasticQuotaImmediateIgnoreTerminatingPod featuregate.Feature = "ElasticQuotaImmediateIgnoreTerminatingPod"

	// ElasticQuotaGuaranteeUsage enable guarantee the quota usage
	// In some specific scenarios, resources that have been allocated to users are considered
	// to belong to the users and will not be preempted back.
	ElasticQuotaGuaranteeUsage featuregate.Feature = "ElasticQuotaGuaranteeUsage"

	// DisableDefaultQuota disable default quota.
	DisableDefaultQuota featuregate.Feature = "DisableDefaultQuota"

	// SupportParentQuotaSubmitPod enables parent Quota submit pod
	SupportParentQuotaSubmitPod featuregate.Feature = "SupportParentQuotaSubmitPod"

	// EnableQuotaAdmission enables quota admission.
	EnableQuotaAdmission featuregate.Feature = "EnableQuotaAdmission"

	// Enable sync GPU shared resource from Device CRD
	EnableSyncGPUSharedResource featuregate.Feature = "EnableSyncGPUSharedResource"
)

var defaultFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	PodMutatingWebhook:                     {Default: true, PreRelease: featuregate.Beta},
	PodValidatingWebhook:                   {Default: true, PreRelease: featuregate.Beta},
	ElasticQuotaMutatingWebhook:            {Default: true, PreRelease: featuregate.Beta},
	ElasticQuotaValidatingWebhook:          {Default: true, PreRelease: featuregate.Beta},
	NodeMutatingWebhook:                    {Default: false, PreRelease: featuregate.Alpha},
	NodeValidatingWebhook:                  {Default: false, PreRelease: featuregate.Alpha},
	ConfigMapValidatingWebhook:             {Default: false, PreRelease: featuregate.Alpha},
	WebhookFramework:                       {Default: true, PreRelease: featuregate.Beta},
	ColocationProfileSkipMutatingResources: {Default: false, PreRelease: featuregate.Alpha},
	MultiQuotaTree:                         {Default: false, PreRelease: featuregate.Alpha},
	ElasticQuotaIgnorePodOverhead:          {Default: false, PreRelease: featuregate.Alpha},
	ElasticQuotaGuaranteeUsage:             {Default: false, PreRelease: featuregate.Alpha},
	DisableDefaultQuota:                    {Default: false, PreRelease: featuregate.Alpha},
	SupportParentQuotaSubmitPod:            {Default: false, PreRelease: featuregate.Alpha},
	EnableQuotaAdmission:                   {Default: false, PreRelease: featuregate.Alpha},
	EnableSyncGPUSharedResource:            {Default: true, PreRelease: featuregate.Alpha},
}

const (
	DisablePVCReservation featuregate.Feature = "DisablePVCReservation"
)

var defaultDeschedulerFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	DisablePVCReservation: {Default: false, PreRelease: featuregate.Beta},
}

const (
	// PriorityTransformer is used to map the pod priority to priority classes defined by Koordinator.
	// If a pod does not set a priorityClass, it will be mapped to the DefaultPriorityClass.
	PriorityTransformer featuregate.Feature = "PriorityTransformer"
	// PreemptionPolicyTransformer is used to take over the pod preemption policy with the specified label.
	// If a pod does not set a preemptionPolicy, it will be mapped to the DefaultPreemptionPolicy.
	PreemptionPolicyTransformer featuregate.Feature = "PreemptionPolicyTransformer"
)

var transformerFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	PriorityTransformer:         {Default: false, PreRelease: featuregate.Alpha},
	PreemptionPolicyTransformer: {Default: false, PreRelease: featuregate.Alpha},
}

func init() {
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(defaultFeatureGates))
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(defaultDeschedulerFeatureGates))
	// TODO: use a unified feature-gate
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(transformerFeatureGates))
}

func SetDefaultFeatureGates() {

}
