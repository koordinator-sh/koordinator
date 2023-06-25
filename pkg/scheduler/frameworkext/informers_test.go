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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	storagev1 "k8s.io/api/storage/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
