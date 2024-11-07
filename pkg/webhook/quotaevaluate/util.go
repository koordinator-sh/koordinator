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

package quotaevaluate

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

func GetQuotaAdmission(quota *v1alpha1.ElasticQuota) (corev1.ResourceList, error) {
	admission, err := extension.GetAdmission(quota)
	if err != nil {
		return nil, err
	}

	// admission is empty (it may not be configured or may have no content), return max
	if len(admission) == 0 {
		return quota.Spec.Max, nil
	}

	return admission, nil
}
