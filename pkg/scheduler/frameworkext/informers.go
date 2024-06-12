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

package frameworkext

import (
	"time"

	policyv1 "k8s.io/api/policy/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	storagev1 "k8s.io/api/storage/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	policyv1informers "k8s.io/client-go/informers/policy/v1"
	policyv1beta1informers "k8s.io/client-go/informers/policy/v1beta1"
	storagev1informers "k8s.io/client-go/informers/storage/v1"
	storagev1beta1informers "k8s.io/client-go/informers/storage/v1beta1"
	clientset "k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/apis/policy"
	policyv1adapt "k8s.io/kubernetes/pkg/apis/policy/v1"
	policyv1beta1adapt "k8s.io/kubernetes/pkg/apis/policy/v1beta1"
	"k8s.io/kubernetes/pkg/apis/storage"
	storagev1adapt "k8s.io/kubernetes/pkg/apis/storage/v1"
	storagev1beta1adapt "k8s.io/kubernetes/pkg/apis/storage/v1beta1"

	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
)

func SetupCustomInformers(informerFactory informers.SharedInformerFactory) {
	if k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.DisableCSIStorageCapacityInformer) {
		// Versions below k8s v1.22 need to disable CSIStorageCapacity
		disableCSIStorageCapacityInformer(informerFactory)
	} else if k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.CompatibleCSIStorageCapacity) &&
		k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.CSIStorageCapacity) {
		// The k8s v1.22 version needs to enable the FeatureGate to convert v1beta1.CSIStorageCapacity to v1.CSIStorageCapacity
		setupCompatibleCSICapacityInformer(informerFactory)
	}

	if k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.DisablePodDisruptionBudgetInformer) {
		disablePodDisruptionBudgetInformer(informerFactory)
	} else if k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.CompatiblePodDisruptionBudget) &&
		k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.PodDisruptionBudget) {
		// Versions below k8s v1.22 need to enable the FeatureGate to convert v1beta1.PodDisruptionBudget to v1.PodDisruptionBudget
		setupCompatiblePodDisruptionBudgetInformer(informerFactory)
	}
}

func disableCSIStorageCapacityInformer(informerFactory informers.SharedInformerFactory) {
	informerFactory.InformerFor(&storagev1beta1.CSIStorageCapacity{}, func(k clientset.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
		fakeClient := kubefake.NewSimpleClientset()
		return storagev1beta1informers.NewFilteredCSIStorageCapacityInformer(fakeClient, metav1.NamespaceAll, resyncPeriod, nil, nil)
	})

	informerFactory.InformerFor(&storagev1.CSIStorageCapacity{}, func(k clientset.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
		fakeClient := kubefake.NewSimpleClientset()
		return storagev1informers.NewFilteredCSIStorageCapacityInformer(fakeClient, metav1.NamespaceAll, resyncPeriod, nil, nil)
	})
}

func setupCompatibleCSICapacityInformer(informerFactory informers.SharedInformerFactory) {
	informerFactory.InformerFor(&storagev1.CSIStorageCapacity{}, newCSIStorageCapacityInformer)
}

func newCSIStorageCapacityInformer(client clientset.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	storageCapacityInformer := storagev1beta1informers.NewFilteredCSIStorageCapacityInformer(client, metav1.NamespaceAll, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, nil)
	if err := storageCapacityInformer.SetTransform(storagev1beta1CSIStorageCapacityTransformer); err != nil {
		klog.Fatalf("Failed to SetTransform with storagev1beta1informer, err: %v", err)
	}
	return storageCapacityInformer
}

func storagev1beta1CSIStorageCapacityTransformer(obj interface{}) (interface{}, error) {
	var storageCapacity *storagev1beta1.CSIStorageCapacity
	switch t := obj.(type) {
	case *storagev1beta1.CSIStorageCapacity:
		storageCapacity = t
	case cache.DeletedFinalStateUnknown:
		storageCapacity, _ = t.Obj.(*storagev1beta1.CSIStorageCapacity)
	}
	if storageCapacity == nil {
		klog.Fatalf("the impossible happened")
	}
	capacity, err := convertV1Beta1CSIStorageCapacityToV1CSIStorageCapacity(storageCapacity)
	if err != nil {
		klog.ErrorS(err, "Failed to convert storagev1beta1.CSIStorageCapacity to storagev1.CSIStorageCapacity")
		return nil, err
	}
	return capacity, nil
}

func convertV1Beta1CSIStorageCapacityToV1CSIStorageCapacity(in *storagev1beta1.CSIStorageCapacity) (*storagev1.CSIStorageCapacity, error) {
	storageCSIStorageCapacity := &storage.CSIStorageCapacity{}
	err := storagev1beta1adapt.Convert_v1beta1_CSIStorageCapacity_To_storage_CSIStorageCapacity(in, storageCSIStorageCapacity, nil)
	if err != nil {
		return nil, err
	}
	out := &storagev1.CSIStorageCapacity{}
	err = storagev1adapt.Convert_storage_CSIStorageCapacity_To_v1_CSIStorageCapacity(storageCSIStorageCapacity, out, nil)
	return out, err
}

func disablePodDisruptionBudgetInformer(informerFactory informers.SharedInformerFactory) {
	informerFactory.InformerFor(&policyv1beta1.PodDisruptionBudget{}, func(k clientset.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
		fakeClient := kubefake.NewSimpleClientset()
		return policyv1beta1informers.NewFilteredPodDisruptionBudgetInformer(fakeClient, metav1.NamespaceAll, resyncPeriod, nil, nil)
	})

	informerFactory.InformerFor(&policyv1.PodDisruptionBudget{}, func(k clientset.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
		fakeClient := kubefake.NewSimpleClientset()
		return policyv1informers.NewFilteredPodDisruptionBudgetInformer(fakeClient, metav1.NamespaceAll, resyncPeriod, nil, nil)
	})
}

func setupCompatiblePodDisruptionBudgetInformer(informerFactory informers.SharedInformerFactory) {
	informerFactory.InformerFor(&policyv1.PodDisruptionBudget{}, newPodDisruptionBudgetInformer)
}

func newPodDisruptionBudgetInformer(client clientset.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	pdbInformer := policyv1beta1informers.NewFilteredPodDisruptionBudgetInformer(client, metav1.NamespaceAll, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, nil)
	if err := pdbInformer.SetTransform(policyv1beta1PodDisruptionBudgetTransformer); err != nil {
		klog.Fatalf("Failed to SetTransform with policyv1beta1informers, err: %v", err)
	}
	return pdbInformer
}

func policyv1beta1PodDisruptionBudgetTransformer(obj interface{}) (interface{}, error) {
	var podDisruptionBudget *policyv1beta1.PodDisruptionBudget
	switch t := obj.(type) {
	case *policyv1beta1.PodDisruptionBudget:
		podDisruptionBudget = t
	case cache.DeletedFinalStateUnknown:
		podDisruptionBudget, _ = t.Obj.(*policyv1beta1.PodDisruptionBudget)
	}
	if podDisruptionBudget == nil {
		klog.Fatalf("the impossible happened")
	}
	out, err := convertV1Beta1PodDisruptionBudgetToV1PodDisruptionBudget(podDisruptionBudget)
	if err != nil {
		klog.ErrorS(err, "Failed to convert policyv1beta1.PodDisruptionBudget to policyv1.PodDisruptionBudget")
		return nil, err
	}
	return out, nil
}

func convertV1Beta1PodDisruptionBudgetToV1PodDisruptionBudget(in *policyv1beta1.PodDisruptionBudget) (*policyv1.PodDisruptionBudget, error) {
	podDisruptionBudget := &policy.PodDisruptionBudget{}
	err := policyv1beta1adapt.Convert_v1beta1_PodDisruptionBudget_To_policy_PodDisruptionBudget(in, podDisruptionBudget, nil)
	if err != nil {
		return nil, err
	}
	out := &policyv1.PodDisruptionBudget{}
	err = policyv1adapt.Convert_policy_PodDisruptionBudget_To_v1_PodDisruptionBudget(podDisruptionBudget, out, nil)
	return out, err
}
