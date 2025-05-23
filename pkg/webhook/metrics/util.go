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

package metrics

const (
	KoordManagerWebhookSubsystem = "koord_manager_webhook"
	ElasticQuotaNameKey          = "elasticquota_name"
	ResourceNameKey              = "resource_name"
	OperationKey                 = "operation"
	PluginNameKey                = "plugin_name"
	StatusKey                    = "status"
	StatusAllowed                = "allowed"
	StatusRejected               = "rejected"
	ObjectTypeKey                = "object_type"
	WebhookTypeKey               = "webhook_type"
	MutatingWebhook              = "mutate"
	ValidatingWebhook            = "validate"
	ConfigMap                    = "configmap"
	ElasticQuota                 = "elasticquota"
	Node                         = "node"
	Pod                          = "pod"
	Reservation                  = "reservation"
)
