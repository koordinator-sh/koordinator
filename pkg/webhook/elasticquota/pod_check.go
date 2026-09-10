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
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

// TODO If the parentQuotaGroup submits pods, the runtime will be calculated incorrectly.
// Supporting the parentQuotaGroup to submit pods is the future work.

func (qt *quotaTopology) ValidateAddPod(pod *corev1.Pod) error {
	qt.lock.Lock()
	defer qt.lock.Unlock()

	featureGate := utilfeature.DefaultFeatureGate
	if featureGate.Enabled(features.SupportParentQuotaSubmitPod) {
		return nil
	}

	quotaName := qt.getQuotaNameFromPodNoLock(pod)
	if quotaName == "" || quotaName == extension.DefaultQuotaName {
		return nil
	}

	quotaInfo := qt.quotaInfoMap[quotaName]
	if quotaInfo.IsParent == true {
		return fmt.Errorf("pod can not be linked to a parentQuotaGroup,quota:%v, pod:%v", quotaName, pod.Name)
	}
	return nil
}

func (qt *quotaTopology) ValidateUpdatePod(oldPod, newPod *corev1.Pod) error {
	if oldPod.Labels[extension.LabelPreemptible] != newPod.Labels[extension.LabelPreemptible] {
		return fmt.Errorf("Preemptible label is forbidden modify now.")
	}
	return qt.ValidateAddPod(newPod)
}

func (qt *quotaTopology) getQuotaNameFromPodNoLock(pod *corev1.Pod) string {
	quotaLabelName := GetQuotaName(pod, qt.client)
	if _, exist := qt.quotaInfoMap[quotaLabelName]; !exist {
		quotaLabelName = extension.DefaultQuotaName
	}
	return quotaLabelName
}

func GetQuotaName(pod *corev1.Pod, kubeClient client.Client) string {
	if name := pod.Annotations[extension.LabelQuotaName]; name != "" {
		return name
	}

	quotaName := extension.GetQuotaName(pod)
	if utilfeature.DefaultFeatureGate.Enabled(features.DisableDefaultQuota) {
		return quotaName
	}

	if quotaName != "" {
		return quotaName
	}

	// Find a quota in the pod's own namespace.
	eqList := &v1alpha1.ElasticQuotaList{}
	if err := kubeClient.List(context.TODO(), eqList, &client.ListOptions{
		Namespace: pod.Namespace,
	}, utilclient.DisableDeepCopy); err != nil {
		klog.Errorf("Failed to list ElasticQuota in namespace %s, err: %v", pod.Namespace, err)
	} else {
		for i := range eqList.Items {
			if eqList.Items[i].Labels[extension.LabelQuotaIsParent] != "true" {
				return eqList.Items[i].Name
			}
		}
	}

	// Find a quota whose AnnotationQuotaNamespaces covers this pod's namespace.
	allEqList := &v1alpha1.ElasticQuotaList{}
	if err := kubeClient.List(context.TODO(), allEqList, utilclient.DisableDeepCopy); err != nil {
		return extension.DefaultQuotaName
	}
	for i := range allEqList.Items {
		for _, ns := range extension.GetAnnotationQuotaNamespaces(&allEqList.Items[i]) {
			if ns == pod.Namespace {
				return allEqList.Items[i].Name
			}
		}
	}

	return extension.DefaultQuotaName
}

// hasQuotaBoundedPods returns true if the quota has bounded pods.
func hasQuotaBoundedPods(kubeClient client.Client, quotaName string, namespaces []string) (bool, error) {
	podList := &corev1.PodList{}
	if err := kubeClient.List(context.TODO(), podList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("label.quotaName", quotaName),
	}, utilclient.DisableDeepCopy); err != nil {
		return false, err
	}
	if len(podList.Items) > 0 {
		return true, nil
	}

	if err := kubeClient.List(context.TODO(), podList, &client.ListOptions{
		Namespace: quotaName,
	}, utilclient.DisableDeepCopy); err != nil {
		return false, err
	}
	if len(podList.Items) > 0 {
		return true, nil
	}

	for _, namespace := range namespaces {
		if err := kubeClient.List(context.TODO(), podList, &client.ListOptions{
			Namespace: namespace,
		}, utilclient.DisableDeepCopy); err != nil {
			return false, err
		}

		if len(podList.Items) > 0 {
			return true, nil
		}
	}

	return false, nil
}
