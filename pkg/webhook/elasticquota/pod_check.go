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

package elasticquota

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

// TODO If the parentQuotaGroup submits pods, the runtime will be calculated incorrectly.
// Supporting the parentQuotaGroup to submit pods is the future work.

func (qt *quotaTopology) ValidateAddPod(pod *corev1.Pod) error {
	qt.lock.Lock()
	defer qt.lock.Unlock()

	quotaName := qt.getQuotaNameFromPodNoLock(pod)
	if quotaName == extension.DefaultQuotaName {
		return nil
	}

	quotaInfo := qt.quotaInfoMap[quotaName]
	if quotaInfo.IsParent == true {
		return fmt.Errorf("pod can not be linked to a parentQuotaGroup,quota:%v, pod:%v", quotaName, pod.Name)
	}
	return nil
}

func (qt *quotaTopology) ValidateUpdatePod(oldPod, newPod *corev1.Pod) error {
	return qt.ValidateAddPod(newPod)
}

func (qt *quotaTopology) getQuotaNameFromPodNoLock(pod *corev1.Pod) string {
	quotaLabelName := pod.Labels[extension.LabelQuotaName]
	if quotaLabelName == "" {
		quotaList := &v1alpha1.ElasticQuotaList{}
		opts := &client.ListOptions{
			Namespace: pod.Namespace,
		}
		err := qt.client.List(context.TODO(), quotaList, opts, utilclient.DisableDeepCopy)
		if err != nil {
			runtime.HandleError(err)
			return extension.DefaultQuotaName
		}
		if len(quotaList.Items) == 0 {
			return extension.DefaultQuotaName
		}
		quotaLabelName = quotaList.Items[0].Name
	}

	if _, exist := qt.quotaInfoMap[quotaLabelName]; !exist {
		quotaLabelName = extension.DefaultQuotaName
	}
	return quotaLabelName
}
