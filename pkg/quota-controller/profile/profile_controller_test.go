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

package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/extension"
	quotav1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	schedv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

func createResourceList(cpu, mem int64) corev1.ResourceList {
	return corev1.ResourceList{
		// use NewMilliQuantity to calculate the runtimeQuota correctly in cpu dimension
		// when the request is smaller than 1 core.
		corev1.ResourceCPU:    *resource.NewMilliQuantity(cpu*1000, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(mem, resource.BinarySI),
	}
}

func createResourceListWithStorage(cpu, mem, storage int64) corev1.ResourceList {
	return corev1.ResourceList{
		// use NewMilliQuantity to calculate the runtimeQuota correctly in cpu dimension
		// when the request is smaller than 1 core.
		corev1.ResourceCPU:     *resource.NewMilliQuantity(cpu*1000, resource.DecimalSI),
		corev1.ResourceMemory:  *resource.NewQuantity(mem, resource.BinarySI),
		corev1.ResourceStorage: *resource.NewQuantity(storage, resource.BinarySI),
	}
}

func defaultCreateNode(nodeName string, labels map[string]string, capacity corev1.ResourceList) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: labels,
		},
		Status: corev1.NodeStatus{
			Allocatable: capacity,
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}

func defaultCreateUnreadyNode(nodeName string, labels map[string]string, capacity corev1.ResourceList) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: labels,
		},
		Status: corev1.NodeStatus{
			Allocatable: capacity,
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}
}

func TestQuotaProfileReconciler_Reconciler_CreateQuota(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	quotav1alpha1.AddToScheme(scheme)
	schedv1alpha1.AddToScheme(scheme)

	nodes := []*corev1.Node{
		defaultCreateNode("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}, createResourceListWithStorage(10, 1000, 1000)),
		defaultCreateUnreadyNode("node2", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}, createResourceListWithStorage(10, 1000, 1000)),
		defaultCreateNode("node3", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-b"}, createResourceListWithStorage(10, 1000, 1000)),
	}

	treeID1 := hash(fmt.Sprintf("%s/%s", "", "profile1"))
	treeID2 := hash(fmt.Sprintf("%s/%s", "", "profile2"))

	resourceRatio := "0.9"

	tests := []struct {
		name                        string
		profile                     *quotav1alpha1.ElasticQuotaProfile
		oriQuota                    *schedv1alpha1.ElasticQuota
		expectQuotaMin              corev1.ResourceList
		expectTotalResource         corev1.ResourceList
		expectUnschedulableResource corev1.ResourceList
		expectQuotaLabels           map[string]string
	}{
		{
			name: "cn-hangzhou-a profile",
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "profile1",
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: "profile1-root",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
				},
			},
			oriQuota:                    nil,
			expectQuotaMin:              createResourceList(20, 2000),
			expectTotalResource:         createResourceListWithStorage(20, 2000, 2000),
			expectUnschedulableResource: createResourceListWithStorage(10, 1000, 1000),
			expectQuotaLabels: map[string]string{
				extension.LabelQuotaProfile: "profile1",
				extension.LabelQuotaTreeID:  treeID1,
				extension.LabelQuotaIsRoot:  "true",
			},
		},
		{
			name: "cn-hangzhou-b profile",
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "profile2",
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: "profile2-root",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-b"},
					},
				},
			},
			oriQuota:                    nil,
			expectQuotaMin:              createResourceList(10, 1000),
			expectTotalResource:         createResourceListWithStorage(10, 1000, 1000),
			expectUnschedulableResource: corev1.ResourceList{},
			expectQuotaLabels: map[string]string{
				extension.LabelQuotaProfile: "profile2",
				extension.LabelQuotaTreeID:  treeID2,
				extension.LabelQuotaIsRoot:  "true",
			},
		},
		{
			name: "more quota labels",
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "profile1",
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: "profile1-root",
					QuotaLabels: map[string]string{
						"topology.kubernetes.io/zone": "cn-hangzhou-a",
					},
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
				},
			},
			oriQuota:                    nil,
			expectQuotaMin:              createResourceList(20, 2000),
			expectTotalResource:         createResourceListWithStorage(20, 2000, 2000),
			expectUnschedulableResource: createResourceListWithStorage(10, 1000, 1000),
			expectQuotaLabels: map[string]string{
				extension.LabelQuotaProfile:   "profile1",
				extension.LabelQuotaTreeID:    treeID1,
				"topology.kubernetes.io/zone": "cn-hangzhou-a",
				extension.LabelQuotaIsRoot:    "true",
			},
		},
		{
			name: "exist quota",
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "profile1",
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: "profile1-root",
					QuotaLabels: map[string]string{
						"topology.kubernetes.io/zone": "cn-hangzhou-a",
					},
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
				},
			},
			oriQuota: &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "profile1-root",
					Labels: map[string]string{"a": "a"},
				},
				Spec: schedv1alpha1.ElasticQuotaSpec{
					Min: createResourceList(5, 50),
				},
			},
			expectQuotaMin:              createResourceList(20, 2000),
			expectTotalResource:         createResourceListWithStorage(20, 2000, 2000),
			expectUnschedulableResource: createResourceListWithStorage(10, 1000, 1000),
			expectQuotaLabels: map[string]string{
				extension.LabelQuotaProfile:   "profile1",
				extension.LabelQuotaTreeID:    treeID1,
				"topology.kubernetes.io/zone": "cn-hangzhou-a",
				"a":                           "a",
				extension.LabelQuotaIsRoot:    "true",
			},
		},
		{
			name: "has ratio",
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "profile1",
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName:     "profile1-root",
					ResourceRatio: &resourceRatio,
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
				},
			},
			oriQuota:                    nil,
			expectQuotaMin:              createResourceList(18, 1800),
			expectTotalResource:         createResourceListWithStorage(18, 1800, 1800),
			expectUnschedulableResource: createResourceListWithStorage(9, 900, 900),
			expectQuotaLabels: map[string]string{
				extension.LabelQuotaProfile: "profile1",
				extension.LabelQuotaTreeID:  treeID1,
				extension.LabelQuotaIsRoot:  "true",
			},
		},
		{
			name: "with tree id",
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "profile1",
					Labels: map[string]string{
						extension.LabelQuotaTreeID: "tree1",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName:     "profile1-root",
					ResourceRatio: &resourceRatio,
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
				},
			},
			oriQuota:                    nil,
			expectQuotaMin:              createResourceList(18, 1800),
			expectTotalResource:         createResourceListWithStorage(18, 1800, 1800),
			expectUnschedulableResource: createResourceListWithStorage(9, 900, 900),
			expectQuotaLabels: map[string]string{
				extension.LabelQuotaProfile: "profile1",
				extension.LabelQuotaTreeID:  "tree1",
				extension.LabelQuotaIsRoot:  "true",
			},
		},
		{
			name: "with resource key",
			profile: &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "profile1",
					Labels: map[string]string{
						extension.LabelQuotaTreeID: "tree1",
					},
					Annotations: map[string]string{
						extension.AnnotationResourceKeys: "[\"cpu\"]",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName:     "profile1-root",
					ResourceRatio: &resourceRatio,
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
				},
			},
			oriQuota:                    nil,
			expectQuotaMin:              corev1.ResourceList{corev1.ResourceCPU: *resource.NewMilliQuantity(18*1000, resource.DecimalSI)},
			expectTotalResource:         createResourceListWithStorage(18, 1800, 1800),
			expectUnschedulableResource: createResourceListWithStorage(9, 900, 900),
			expectQuotaLabels: map[string]string{
				extension.LabelQuotaProfile: "profile1",
				extension.LabelQuotaTreeID:  "tree1",
				extension.LabelQuotaIsRoot:  "true",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &QuotaProfileReconciler{
				Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
				Scheme: scheme,
			}
			// create node
			for _, node := range nodes {
				nodeCopy := node.DeepCopy()
				err := r.Client.Create(context.TODO(), nodeCopy)
				assert.NoError(t, err)
			}

			profileReq := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: tc.profile.Namespace, Name: tc.profile.Name}}

			err := r.Client.Create(context.TODO(), tc.profile)
			assert.NoError(t, err)
			if tc.oriQuota != nil {
				err := r.Client.Create(context.TODO(), tc.oriQuota)
				assert.NoError(t, err)
			}

			r.Reconcile(context.TODO(), profileReq)
			quota := &schedv1alpha1.ElasticQuota{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: tc.profile.Namespace, Name: tc.profile.Spec.QuotaName}, quota)
			assert.NoError(t, err)

			total := corev1.ResourceList{}
			err = json.Unmarshal([]byte(quota.Annotations[extension.AnnotationTotalResource]), &total)
			assert.NoError(t, err)

			unschedulable, err := extension.GetUnschedulableResource(quota)
			assert.NoError(t, err)

			assert.True(t, quotav1.Equals(tc.expectQuotaMin, quota.Spec.Min))
			assert.True(t, quotav1.Equals(tc.expectTotalResource, total))
			assert.True(t, quotav1.Equals(tc.expectUnschedulableResource, unschedulable))
			assert.Equal(t, tc.expectQuotaLabels, quota.Labels)
		})
	}
}

func TestMultiplyQuantity(t *testing.T) {
	tests := []struct {
		name         string
		resourceName corev1.ResourceName
		value        resource.Quantity
		ratio        float64
		expectValue  resource.Quantity
	}{
		{
			name:         "basic cpu 1",
			resourceName: corev1.ResourceCPU,
			value:        resource.MustParse("1"),
			ratio:        0.9,
			expectValue:  resource.MustParse("0.9"),
		},
		{
			name:         "basic cpu 2",
			resourceName: corev1.ResourceCPU,
			value:        resource.MustParse("100m"),
			ratio:        0.5,
			expectValue:  resource.MustParse("50m"),
		},
		{
			name:         "basic memory 1",
			resourceName: corev1.ResourceCPU,
			value:        resource.MustParse("1Gi"),
			ratio:        0.9,
			expectValue:  resource.MustParse("0.9Gi"),
		},
		{
			name:         "basic memory 2",
			resourceName: corev1.ResourceCPU,
			value:        resource.MustParse("1Gi"),
			ratio:        0.5,
			expectValue:  resource.MustParse("512Mi"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := MultiplyQuantity(tc.value, tc.resourceName, tc.ratio)
			assert.Equal(t, tc.expectValue.MilliValue(), target.MilliValue())
		})
	}
}
