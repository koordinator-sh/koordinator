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

package informer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	policyv1 "k8s.io/api/policy/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	storagev1 "k8s.io/api/storage/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"

	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func TestCustomCSIStorageCapacityInformer(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.CompatibleCSIStorageCapacity, true)()

	fakeClient := kubefake.NewSimpleClientset()
	storageCapacity := &storagev1beta1.CSIStorageCapacity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		NodeTopology: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"key-1": "value-1",
			},
		},
		StorageClassName:  "test-sc",
		Capacity:          resource.NewQuantity(200*1024*1024*1024, resource.BinarySI),
		MaximumVolumeSize: resource.NewQuantity(10, resource.DecimalSI),
	}
	_, err := fakeClient.StorageV1beta1().CSIStorageCapacities(storageCapacity.Namespace).Create(context.TODO(), storageCapacity, metav1.CreateOptions{})
	assert.NoError(t, err)

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	SetupCustomInformers(informerFactory)

	capacityLister := informerFactory.Storage().V1().CSIStorageCapacities().Lister()
	assert.NotNil(t, capacityLister)

	informerFactory.Start(nil)
	informerFactory.WaitForCacheSync(nil)

	got, err := capacityLister.CSIStorageCapacities(storageCapacity.Namespace).Get(storageCapacity.Name)
	assert.NoError(t, err)

	expected := &storagev1.CSIStorageCapacity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		NodeTopology: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"key-1": "value-1",
			},
		},
		StorageClassName:  "test-sc",
		Capacity:          resource.NewQuantity(200*1024*1024*1024, resource.BinarySI),
		MaximumVolumeSize: resource.NewQuantity(10, resource.DecimalSI),
	}
	assert.Equal(t, expected, got)
}

func TestCustomDisableCSIStorageCapacityInformer(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.DisableCSIStorageCapacityInformer, true)()

	fakeClient := kubefake.NewSimpleClientset()
	storageCapacity := &storagev1beta1.CSIStorageCapacity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		NodeTopology: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"key-1": "value-1",
			},
		},
		StorageClassName:  "test-sc",
		Capacity:          resource.NewQuantity(200*1024*1024*1024, resource.BinarySI),
		MaximumVolumeSize: resource.NewQuantity(10, resource.DecimalSI),
	}
	_, err := fakeClient.StorageV1beta1().CSIStorageCapacities(storageCapacity.Namespace).Create(context.TODO(), storageCapacity, metav1.CreateOptions{})
	assert.NoError(t, err)

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	SetupCustomInformers(informerFactory)

	capacityLister := informerFactory.Storage().V1().CSIStorageCapacities().Lister()
	assert.NotNil(t, capacityLister)

	informerFactory.Start(nil)
	informerFactory.WaitForCacheSync(nil)

	got, err := capacityLister.CSIStorageCapacities(storageCapacity.Namespace).Get(storageCapacity.Name)
	assert.True(t, errors.IsNotFound(err))
	assert.Nil(t, got)
}

func TestCustomPodDisruptionBudgetInformer(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.CompatiblePodDisruptionBudget, true)()

	fakeClient := kubefake.NewSimpleClientset()
	now := time.Now()
	pdb := &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: policyv1beta1.PodDisruptionBudgetSpec{
			MinAvailable: intstr.ValueOrDefault(nil, intstr.FromInt(10)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test": "true",
				},
			},
			MaxUnavailable: intstr.ValueOrDefault(nil, intstr.FromInt(1)),
		},
		Status: policyv1beta1.PodDisruptionBudgetStatus{
			ObservedGeneration: 1,
			DisruptedPods: map[string]metav1.Time{
				"123456": {Time: now},
			},
			DisruptionsAllowed: 1,
			CurrentHealthy:     2,
			DesiredHealthy:     3,
			ExpectedPods:       4,
			Conditions: []metav1.Condition{
				{
					Type:   policyv1beta1.DisruptionAllowedCondition,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	_, err := fakeClient.PolicyV1beta1().PodDisruptionBudgets(pdb.Namespace).Create(context.TODO(), pdb, metav1.CreateOptions{})
	assert.NoError(t, err)

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	SetupCustomInformers(informerFactory)

	v1PDBLister := informerFactory.Policy().V1().PodDisruptionBudgets().Lister()
	assert.NotNil(t, v1PDBLister)

	informerFactory.Start(nil)
	informerFactory.WaitForCacheSync(nil)

	got, err := v1PDBLister.PodDisruptionBudgets(pdb.Namespace).Get(pdb.Name)
	assert.NoError(t, err)

	expected := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: intstr.ValueOrDefault(nil, intstr.FromInt(10)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test": "true",
				},
			},
			MaxUnavailable: intstr.ValueOrDefault(nil, intstr.FromInt(1)),
		},
		Status: policyv1.PodDisruptionBudgetStatus{
			ObservedGeneration: 1,
			DisruptedPods: map[string]metav1.Time{
				"123456": {Time: now},
			},
			DisruptionsAllowed: 1,
			CurrentHealthy:     2,
			DesiredHealthy:     3,
			ExpectedPods:       4,
			Conditions: []metav1.Condition{
				{
					Type:   policyv1beta1.DisruptionAllowedCondition,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	assert.Equal(t, expected, got)
}

func TestCustomDisablePodDisruptionBudgetInformer(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.DisablePodDisruptionBudgetInformer, true)()

	fakeClient := kubefake.NewSimpleClientset()
	now := time.Now()
	pdb := &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: policyv1beta1.PodDisruptionBudgetSpec{
			MinAvailable: intstr.ValueOrDefault(nil, intstr.FromInt(10)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test": "true",
				},
			},
			MaxUnavailable: intstr.ValueOrDefault(nil, intstr.FromInt(1)),
		},
		Status: policyv1beta1.PodDisruptionBudgetStatus{
			ObservedGeneration: 1,
			DisruptedPods: map[string]metav1.Time{
				"123456": {Time: now},
			},
			DisruptionsAllowed: 1,
			CurrentHealthy:     2,
			DesiredHealthy:     3,
			ExpectedPods:       4,
			Conditions: []metav1.Condition{
				{
					Type:   policyv1beta1.DisruptionAllowedCondition,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	_, err := fakeClient.PolicyV1beta1().PodDisruptionBudgets(pdb.Namespace).Create(context.TODO(), pdb, metav1.CreateOptions{})
	assert.NoError(t, err)

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	SetupCustomInformers(informerFactory)

	v1PDBLister := informerFactory.Policy().V1().PodDisruptionBudgets().Lister()
	assert.NotNil(t, v1PDBLister)

	informerFactory.Start(nil)
	informerFactory.WaitForCacheSync(nil)

	got, err := v1PDBLister.PodDisruptionBudgets(pdb.Namespace).Get(pdb.Name)
	assert.True(t, errors.IsNotFound(err))
	assert.Nil(t, got)
}
