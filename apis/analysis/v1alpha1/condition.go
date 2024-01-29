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

package v1alpha1

const (
	// LowConfidenceCondition indicates the low confidence for the current forecasting result.
	LowConfidenceCondition string = "LowConfidence"
	// NoObjectsMatchedCondition indicates that the current description didn't match any objects.
	NoObjectsMatchedCondition string = "NoObjectsMatched"
	// FetchingHistoryCondition indicates that forecaster is in the process of loading additional
	// history samples.
	FetchingHistoryCondition string = "FetchingHistory"
	// ConfigDeprecatedCondition indicates that this configuration is deprecated and will stop being
	// supported soon.
	ConfigDeprecatedCondition string = "ConfigDeprecated"
	// ConfigUnsupportedCondition indicates that this configuration is unsupported and will not be provided for it.
	ConfigUnsupportedCondition string = "ConfigUnsupported"
)
