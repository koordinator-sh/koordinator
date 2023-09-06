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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
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
	quotaLabelName := GetQuotaName(pod, qt.client)
	if _, exist := qt.quotaInfoMap[quotaLabelName]; !exist {
		quotaLabelName = extension.DefaultQuotaName
	}
	return quotaLabelName
}

func GetQuotaName(pod *corev1.Pod, kubeClient client.Client) string {
	quotaName := extension.GetQuotaName(pod)
	if quotaName != "" {
		return quotaName
	}

	eq := &v1alpha1.ElasticQuota{}
	err := kubeClient.Get(context.TODO(), types.NamespacedName{Namespace: pod.Namespace, Name: pod.Namespace}, eq)
	if err == nil {
		return eq.Name
	} else if !errors.IsNotFound(err) {
		klog.Errorf("Failed to Get ElasticQuota %s, err: %v", pod.Namespace, err)
	}

	eqList := &v1alpha1.ElasticQuotaList{}
	if err := kubeClient.List(context.TODO(), eqList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("annotation.namespaces", pod.Namespace),
	}, utilclient.DisableDeepCopy); err != nil {
		return extension.DefaultQuotaName
	}
	for _, quota := range eqList.Items {
		return quota.Name
	}

	return extension.DefaultQuotaName
}

func ListQuotaBoundPods(kubeClient client.Client, quota *v1alpha1.ElasticQuota) (*corev1.PodList, error) {
	podList := &corev1.PodList{}
	if err := kubeClient.List(context.TODO(), podList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("label.quotaName", quota.Name),
	}, utilclient.DisableDeepCopy); err != nil {
		return nil, err
	}
	if len(podList.Items) > 0 {
		return podList, nil
	}

	if err := kubeClient.List(context.TODO(), podList, &client.ListOptions{
		Namespace: quota.Name,
	}, utilclient.DisableDeepCopy); err != nil {
		return nil, err
	}
	if len(podList.Items) > 0 {
		return podList, nil
	}

	namespaces := extension.GetAnnotationQuotaNamespaces(quota)
	for _, namespace := range namespaces {
		if err := kubeClient.List(context.TODO(), podList, &client.ListOptions{
			Namespace: namespace,
		}, utilclient.DisableDeepCopy); err != nil {
			return nil, err
		}

		if len(podList.Items) > 0 {
			return podList, nil
		}
	}

	return nil, nil
}
