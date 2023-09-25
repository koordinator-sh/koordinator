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

package arbitrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/clock"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config/v1alpha2"
)

func TestFilterExistingMigrationJob(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	a := filter{client: fakeClient, args: &config.MigrationControllerArgs{}, arbitratedPodMigrationJobs: map[types.UID]bool{}}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	assert.Nil(t, a.client.Create(context.TODO(), pod))

	job := &v1alpha1.PodMigrationJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test",
			CreationTimestamp: metav1.Time{Time: time.Now()},
		},
		Spec: v1alpha1.PodMigrationJobSpec{
			PodRef: &corev1.ObjectReference{
				Namespace: "default",
				Name:      "test-pod",
				UID:       pod.UID,
			},
		},
	}
	assert.Nil(t, a.client.Create(context.TODO(), job))

	assert.False(t, a.filterExistingPodMigrationJob(pod))
}

func TestFilterMaxMigratingPerNode(t *testing.T) {
	tests := []struct {
		name             string
		numMigratingPods int
		samePod          bool
		sameNode         bool
		maxMigrating     int32
		want             bool
	}{
		{
			name: "maxMigrating=0",
			want: true,
		},
		{
			name:         "maxMigrating=1 no migrating Pods",
			maxMigrating: 1,
			want:         true,
		},
		{
			name:             "maxMigrating=1 one migrating Pod with same Pod and Node",
			numMigratingPods: 1,
			samePod:          true,
			sameNode:         true,
			maxMigrating:     1,
			want:             true,
		},
		{
			name:             "maxMigrating=1 one migrating Pod with diff Pod and same Node",
			numMigratingPods: 1,
			samePod:          false,
			sameNode:         true,
			maxMigrating:     1,
			want:             false,
		},
		{
			name:             "maxMigrating=1 one migrating Pod with diff Pod and Node",
			numMigratingPods: 1,
			samePod:          false,
			sameNode:         false,
			maxMigrating:     1,
			want:             true,
		},
		{
			name:             "maxMigrating=2 two migrating Pod with same Pod and Node",
			numMigratingPods: 2,
			samePod:          true,
			sameNode:         true,
			maxMigrating:     2,
			want:             true,
		},
		{
			name:             "maxMigrating=2 two migrating Pod with diff Pod and Node",
			numMigratingPods: 2,
			samePod:          false,
			sameNode:         false,
			maxMigrating:     2,
			want:             true,
		},
		{
			name:             "maxMigrating=2 two migrating Pod with diff Pod and same Node",
			numMigratingPods: 2,
			samePod:          false,
			sameNode:         true,
			maxMigrating:     2,
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			a := filter{client: fakeClient, args: &config.MigrationControllerArgs{}, arbitratedPodMigrationJobs: map[types.UID]bool{}}
			a.args.MaxMigratingPerNode = pointer.Int32(tt.maxMigrating)

			var migratingPods []*corev1.Pod
			for i := 0; i < tt.numMigratingPods; i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      fmt.Sprintf("test-migrating-pod-%d", i),
						UID:       uuid.NewUUID(),
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				}
				migratingPods = append(migratingPods, pod)

				assert.Nil(t, a.client.Create(context.TODO(), pod))

				job := &v1alpha1.PodMigrationJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:              fmt.Sprintf("test-%d", i),
						CreationTimestamp: metav1.Time{Time: time.Now()},
						Annotations:       map[string]string{AnnotationPassedArbitration: "true"},
						UID:               uuid.NewUUID(),
					},
					Spec: v1alpha1.PodMigrationJobSpec{
						PodRef: &corev1.ObjectReference{
							Namespace: pod.Namespace,
							Name:      pod.Name,
							UID:       pod.UID,
						},
					},
				}
				a.markJobPassedArbitration(job.UID)
				assert.Nil(t, a.client.Create(context.TODO(), job))
			}

			var filterPod *corev1.Pod
			if tt.samePod && len(migratingPods) > 0 {
				filterPod = migratingPods[0]
			}
			if filterPod == nil {
				filterPod = &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      fmt.Sprintf("test-pod-%s", uuid.NewUUID()),
						UID:       uuid.NewUUID(),
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				}
			}
			if tt.sameNode {
				filterPod.Spec.NodeName = "test-node"
			} else {
				filterPod.Spec.NodeName = "test-other-node"
			}

			got := a.filterMaxMigratingPerNode(filterPod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFilterMaxMigratingPerNamespace(t *testing.T) {
	tests := []struct {
		name             string
		numMigratingPods int
		samePod          bool
		sameNamespace    bool
		maxMigrating     int32
		want             bool
	}{
		{
			name: "maxMigrating=0",
			want: true,
		},
		{
			name:         "maxMigrating=1 no migrating Pods",
			maxMigrating: 1,
			want:         true,
		},
		{
			name:             "maxMigrating=1 one migrating Pod with same Pod and Namespace",
			numMigratingPods: 1,
			samePod:          true,
			sameNamespace:    true,
			maxMigrating:     1,
			want:             true,
		},
		{
			name:             "maxMigrating=1 one migrating Pod with diff Pod and same Namespace",
			numMigratingPods: 1,
			samePod:          false,
			sameNamespace:    true,
			maxMigrating:     1,
			want:             false,
		},
		{
			name:             "maxMigrating=1 one migrating Pod with diff Pod and Namespace",
			numMigratingPods: 1,
			samePod:          false,
			sameNamespace:    false,
			maxMigrating:     1,
			want:             true,
		},
		{
			name:             "maxMigrating=2 two migrating Pod with same Pod and Namespace",
			numMigratingPods: 2,
			samePod:          true,
			sameNamespace:    true,
			maxMigrating:     2,
			want:             true,
		},
		{
			name:             "maxMigrating=2 two migrating Pod with diff Pod and Namespace",
			numMigratingPods: 2,
			samePod:          false,
			sameNamespace:    false,
			maxMigrating:     2,
			want:             true,
		},
		{
			name:             "maxMigrating=2 two migrating Pod with diff Pod and same Namespace",
			numMigratingPods: 2,
			samePod:          false,
			sameNamespace:    true,
			maxMigrating:     2,
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			a := filter{client: fakeClient, args: &config.MigrationControllerArgs{}, arbitratedPodMigrationJobs: map[types.UID]bool{}}
			a.args.MaxMigratingPerNamespace = pointer.Int32(tt.maxMigrating)

			var migratingPods []*corev1.Pod
			for i := 0; i < tt.numMigratingPods; i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      fmt.Sprintf("test-migrating-pod-%d", i),
						UID:       uuid.NewUUID(),
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				}
				migratingPods = append(migratingPods, pod)

				assert.Nil(t, a.client.Create(context.TODO(), pod))

				job := &v1alpha1.PodMigrationJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:              fmt.Sprintf("test-%d", i),
						CreationTimestamp: metav1.Time{Time: time.Now()},
						Annotations:       map[string]string{AnnotationPassedArbitration: "true"},
						UID:               uuid.NewUUID(),
					},
					Spec: v1alpha1.PodMigrationJobSpec{
						PodRef: &corev1.ObjectReference{
							Namespace: pod.Namespace,
							Name:      pod.Name,
							UID:       pod.UID,
						},
					},
				}
				a.markJobPassedArbitration(job.UID)
				assert.Nil(t, a.client.Create(context.TODO(), job))
			}

			var filterPod *corev1.Pod
			if tt.samePod && len(migratingPods) > 0 {
				filterPod = migratingPods[0]
			}
			if filterPod == nil {
				filterPod = &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      fmt.Sprintf("test-pod-%s", uuid.NewUUID()),
						UID:       uuid.NewUUID(),
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				}
			}
			if !tt.sameNamespace {
				filterPod.Namespace = "other-namespace"
			}
			got := a.filterMaxMigratingPerNamespace(filterPod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFilterMaxMigratingPerWorkload(t *testing.T) {
	ownerReferences1 := []metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Controller: pointer.Bool(true),
			Kind:       "StatefulSet",
			Name:       "test-1",
			UID:        uuid.NewUUID(),
		},
	}

	ownerReferences2 := []metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Controller: pointer.Bool(true),
			Kind:       "StatefulSet",
			Name:       "test-2",
			UID:        uuid.NewUUID(),
		},
	}
	tests := []struct {
		name             string
		totalReplicas    int32
		numMigratingPods int
		samePod          bool
		sameWorkload     bool
		maxMigrating     int
		want             bool
	}{
		{
			name:             "totalReplicas=10 and maxMigrating=1 no migrating Pod",
			totalReplicas:    10,
			numMigratingPods: 0,
			maxMigrating:     1,
			samePod:          false,
			sameWorkload:     false,
			want:             true,
		},
		{
			name:             "totalReplicas=10 and maxMigrating=1 one migrating Pod with same Pod and Workload",
			totalReplicas:    10,
			numMigratingPods: 1,
			maxMigrating:     1,
			samePod:          true,
			sameWorkload:     true,
			want:             true,
		},
		{
			name:             "totalReplicas=10 and maxMigrating=1 one migrating Pod with diff Pod and same Workload",
			totalReplicas:    10,
			numMigratingPods: 1,
			maxMigrating:     1,
			samePod:          false,
			sameWorkload:     true,
			want:             false,
		},
		{
			name:             "totalReplicas=10 and maxMigrating=1 one migrating Pod with diff Pod and diff Workload",
			totalReplicas:    10,
			numMigratingPods: 1,
			maxMigrating:     1,
			samePod:          false,
			sameWorkload:     false,
			want:             true,
		},
		{
			name:             "totalReplicas=10 and maxMigrating=2 two migrating Pod with same Pod and Workload",
			totalReplicas:    10,
			numMigratingPods: 2,
			maxMigrating:     2,
			samePod:          true,
			sameWorkload:     true,
			want:             true,
		},
		{
			name:             "totalReplicas=10 and maxMigrating=2 two migrating Pod with diff Pod and same Workload",
			totalReplicas:    10,
			numMigratingPods: 2,
			maxMigrating:     2,
			samePod:          false,
			sameWorkload:     true,
			want:             false,
		},
		{
			name:             "totalReplicas=10 and maxMigrating=2 two migrating Pod with diff Pod and diff Workload",
			totalReplicas:    10,
			numMigratingPods: 2,
			maxMigrating:     2,
			samePod:          false,
			sameWorkload:     false,
			want:             true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			intOrString := intstr.FromInt(tt.maxMigrating)
			maxUnavailable := intstr.FromInt(int(tt.totalReplicas - 1))

			a := filter{client: fakeClient, args: &config.MigrationControllerArgs{}, arbitratedPodMigrationJobs: map[types.UID]bool{}}
			a.args.MaxMigratingPerWorkload = &intOrString
			a.args.MaxUnavailablePerWorkload = &maxUnavailable

			var migratingPods []*corev1.Pod
			for i := 0; i < tt.numMigratingPods; i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "default",
						Name:            fmt.Sprintf("test-migrating-pod-%d", i),
						UID:             uuid.NewUUID(),
						OwnerReferences: ownerReferences1,
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.PodReady,
								Status: corev1.ConditionTrue,
							},
						},
					},
				}
				migratingPods = append(migratingPods, pod)

				assert.Nil(t, a.client.Create(context.TODO(), pod))

				job := &v1alpha1.PodMigrationJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:              fmt.Sprintf("test-%d", i),
						CreationTimestamp: metav1.Time{Time: time.Now()},
						Annotations:       map[string]string{AnnotationPassedArbitration: "true"},
					},
					Spec: v1alpha1.PodMigrationJobSpec{
						PodRef: &corev1.ObjectReference{
							Namespace: pod.Namespace,
							Name:      pod.Name,
							UID:       pod.UID,
						},
					},
				}
				a.markJobPassedArbitration(job.UID)
				assert.Nil(t, a.client.Create(context.TODO(), job))
			}

			var filterPod *corev1.Pod
			if tt.samePod && len(migratingPods) > 0 {
				filterPod = migratingPods[0]
			}
			if filterPod == nil {
				filterPod = &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "default",
						Name:            fmt.Sprintf("test-pod-%s", uuid.NewUUID()),
						UID:             uuid.NewUUID(),
						OwnerReferences: ownerReferences1,
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				}
			}
			if !tt.sameWorkload {
				filterPod.OwnerReferences = ownerReferences2
			}

			a.controllerFinder = &fakeControllerFinder{
				replicas: tt.totalReplicas,
			}

			got := a.filterMaxMigratingOrUnavailablePerWorkload(filterPod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFilterMaxUnavailablePerWorkload(t *testing.T) {
	ownerReferences1 := []metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Controller: pointer.Bool(true),
			Kind:       "StatefulSet",
			Name:       "test-1",
			UID:        uuid.NewUUID(),
		},
	}

	ownerReferences2 := []metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Controller: pointer.Bool(true),
			Kind:       "StatefulSet",
			Name:       "test-2",
			UID:        uuid.NewUUID(),
		},
	}
	tests := []struct {
		name               string
		totalReplicas      int32
		numUnavailablePods int
		numMigratingPods   int
		maxUnavailable     int
		sameWorkload       bool
		want               bool
	}{
		{
			name:               "totalReplicas=10 and maxUnavailable=1 no migrating Pod and no unavailable Pod",
			totalReplicas:      10,
			numUnavailablePods: 0,
			numMigratingPods:   0,
			maxUnavailable:     1,
			sameWorkload:       true,
			want:               true,
		},
		{
			name:               "totalReplicas=10 and maxUnavailable=1 one unavailable Pod with same Workload",
			totalReplicas:      10,
			numUnavailablePods: 1,
			numMigratingPods:   0,
			maxUnavailable:     1,
			sameWorkload:       true,
			want:               false,
		},
		{
			name:               "totalReplicas=10 and maxUnavailable=1 one migrating Pod with same Workload",
			totalReplicas:      10,
			numUnavailablePods: 0,
			numMigratingPods:   1,
			maxUnavailable:     1,
			sameWorkload:       true,
			want:               false,
		},
		{
			name:               "totalReplicas=10 and maxUnavailable=1 one unavailable Pod and one migrating Pod with same Workload",
			totalReplicas:      10,
			numUnavailablePods: 1,
			maxUnavailable:     1,
			sameWorkload:       true,
			want:               false,
		},

		{
			name:               "totalReplicas=10 and maxUnavailable=2 no migrating Pod and no unavailable Pod",
			totalReplicas:      10,
			numUnavailablePods: 0,
			numMigratingPods:   0,
			maxUnavailable:     2,
			sameWorkload:       true,
			want:               true,
		},
		{
			name:               "totalReplicas=10 and maxUnavailable=2 one unavailable Pod with same Workload",
			totalReplicas:      10,
			numUnavailablePods: 1,
			numMigratingPods:   0,
			maxUnavailable:     2,
			sameWorkload:       true,
			want:               true,
		},
		{
			name:               "totalReplicas=10 and maxUnavailable=2 one migrating Pod with same Workload",
			totalReplicas:      10,
			numUnavailablePods: 0,
			numMigratingPods:   1,
			maxUnavailable:     2,
			sameWorkload:       true,
			want:               true,
		},
		{
			name:               "totalReplicas=10 and maxUnavailable=2 one unavailable Pod and one migrating Pod with same Workload",
			totalReplicas:      10,
			numUnavailablePods: 1,
			numMigratingPods:   1,
			maxUnavailable:     2,
			sameWorkload:       true,
			want:               false,
		},
		{
			name:               "totalReplicas=10 and maxUnavailable=2 one unavailable Pod and one migrating Pod with diff Workload",
			totalReplicas:      10,
			numUnavailablePods: 1,
			numMigratingPods:   1,
			maxUnavailable:     2,
			sameWorkload:       false,
			want:               true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intOrString := intstr.FromInt(int(tt.totalReplicas - 1))
			maxUnavailable := intstr.FromInt(tt.maxUnavailable)

			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			a := filter{client: fakeClient, args: &config.MigrationControllerArgs{}, arbitratedPodMigrationJobs: map[types.UID]bool{}}
			a.args.MaxMigratingPerWorkload = &intOrString
			a.args.MaxUnavailablePerWorkload = &maxUnavailable

			var totalPods []*corev1.Pod
			for i := 0; i < tt.numUnavailablePods; i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "default",
						Name:            fmt.Sprintf("test-unavailable-pod-%d", i),
						UID:             uuid.NewUUID(),
						OwnerReferences: ownerReferences1,
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
					},
				}
				totalPods = append(totalPods, pod)

				assert.Nil(t, a.client.Create(context.TODO(), pod))
			}

			for i := 0; i < tt.numMigratingPods; i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "default",
						Name:            fmt.Sprintf("test-migrating-pod-%d", i),
						UID:             uuid.NewUUID(),
						OwnerReferences: ownerReferences1,
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.PodReady,
								Status: corev1.ConditionTrue,
							},
						},
					},
				}
				totalPods = append(totalPods, pod)

				assert.Nil(t, a.client.Create(context.TODO(), pod))

				job := &v1alpha1.PodMigrationJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:              fmt.Sprintf("test-%d", i),
						CreationTimestamp: metav1.Time{Time: time.Now()},
						Annotations:       map[string]string{AnnotationPassedArbitration: "true"},
					},
					Spec: v1alpha1.PodMigrationJobSpec{
						PodRef: &corev1.ObjectReference{
							Namespace: pod.Namespace,
							Name:      pod.Name,
							UID:       pod.UID,
						},
					},
				}
				a.markJobPassedArbitration(job.UID)
				assert.Nil(t, a.client.Create(context.TODO(), job))
			}

			for i := 0; i < int(tt.totalReplicas)-len(totalPods); i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "default",
						Name:            fmt.Sprintf("test-available-pod-%d", i),
						UID:             uuid.NewUUID(),
						OwnerReferences: ownerReferences1,
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.PodReady,
								Status: corev1.ConditionTrue,
							},
						},
					},
				}
				totalPods = append(totalPods, pod)
			}

			filterPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       "default",
					Name:            fmt.Sprintf("test-pod-%s", uuid.NewUUID()),
					UID:             uuid.NewUUID(),
					OwnerReferences: ownerReferences1,
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			if !tt.sameWorkload {
				filterPod.OwnerReferences = ownerReferences2
			}

			a.controllerFinder = &fakeControllerFinder{
				pods:     totalPods,
				replicas: tt.totalReplicas,
			}

			got := a.filterMaxMigratingOrUnavailablePerWorkload(filterPod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFilterExpectedReplicas(t *testing.T) {
	ownerReferences1 := []metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Controller: pointer.Bool(true),
			Kind:       "StatefulSet",
			Name:       "test-1",
			UID:        uuid.NewUUID(),
		},
	}

	ownerReferences2 := []metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Controller: pointer.Bool(true),
			Kind:       "StatefulSet",
			Name:       "test-2",
			UID:        uuid.NewUUID(),
		},
	}
	tests := []struct {
		name               string
		totalReplicas      int32
		numUnavailablePods int
		maxUnavailable     int
		prepareMigrating   bool
		sameWorkload       bool
		want               bool
	}{
		{
			name:               "totalReplicas=1 and maxUnavailable=1",
			totalReplicas:      1,
			numUnavailablePods: 0,
			maxUnavailable:     1,
			sameWorkload:       true,
			want:               false,
		},
		{
			name:               "totalReplicas=100 and maxUnavailable=100",
			totalReplicas:      100,
			numUnavailablePods: 0,
			maxUnavailable:     100,
			sameWorkload:       true,
			want:               false,
		},
		{
			name:               "totalReplicas=100 and maxUnavailable=10",
			totalReplicas:      100,
			numUnavailablePods: 0,
			maxUnavailable:     10,
			sameWorkload:       true,
			want:               true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intOrString := intstr.FromInt(int(tt.totalReplicas - 1))
			maxUnavailable := intstr.FromInt(tt.maxUnavailable)

			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			a := filter{client: fakeClient, args: &config.MigrationControllerArgs{}}
			a.args.MaxMigratingPerWorkload = &intOrString
			a.args.MaxUnavailablePerWorkload = &maxUnavailable

			var totalPods []*corev1.Pod
			for i := 0; i < tt.numUnavailablePods; i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "default",
						Name:            fmt.Sprintf("test-unavailable-pod-%d", i),
						UID:             uuid.NewUUID(),
						OwnerReferences: ownerReferences1,
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
					},
				}
				totalPods = append(totalPods, pod)

				assert.Nil(t, a.client.Create(context.TODO(), pod))
			}

			for i := 0; i < int(tt.totalReplicas)-len(totalPods); i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "default",
						Name:            fmt.Sprintf("test-available-pod-%d", i),
						UID:             uuid.NewUUID(),
						OwnerReferences: ownerReferences1,
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.PodReady,
								Status: corev1.ConditionTrue,
							},
						},
					},
				}
				totalPods = append(totalPods, pod)
			}

			filterPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       "default",
					Name:            fmt.Sprintf("test-pod-%s", uuid.NewUUID()),
					UID:             uuid.NewUUID(),
					OwnerReferences: ownerReferences1,
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}
			if !tt.sameWorkload {
				filterPod.OwnerReferences = ownerReferences2
			}

			a.controllerFinder = &fakeControllerFinder{
				pods:     totalPods,
				replicas: tt.totalReplicas,
			}

			got := a.filterExpectedReplicas(filterPod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFilterObjectLimiter(t *testing.T) {
	ownerReferences1 := []metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Controller: pointer.Bool(true),
			Kind:       "StatefulSet",
			Name:       "test-1",
			UID:        uuid.NewUUID(),
		},
	}
	otherOwnerReferences := metav1.OwnerReference{
		APIVersion: "apps/v1",
		Controller: pointer.Bool(true),
		Kind:       "StatefulSet",
		Name:       "test-2",
		UID:        uuid.NewUUID(),
	}
	testObjectLimiters := config.ObjectLimiterMap{
		config.MigrationLimitObjectWorkload: {
			Duration:     metav1.Duration{Duration: 1 * time.Second},
			MaxMigrating: &intstr.IntOrString{Type: intstr.Int, IntVal: 10},
		},
	}

	tests := []struct {
		name             string
		objectLimiters   config.ObjectLimiterMap
		totalReplicas    int32
		sleepDuration    time.Duration
		pod              *corev1.Pod
		evictedPodsCount int
		evictedWorkload  *metav1.OwnerReference
		want             bool
	}{
		{
			name:           "less than default maxMigrating",
			totalReplicas:  100,
			objectLimiters: testObjectLimiters,
			sleepDuration:  100 * time.Millisecond,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: ownerReferences1,
				},
			},
			evictedPodsCount: 6,
			want:             true,
		},
		{
			name:           "exceeded default maxMigrating",
			totalReplicas:  100,
			objectLimiters: testObjectLimiters,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: ownerReferences1,
				},
			},
			evictedPodsCount: 11,
			want:             false,
		},
		{
			name:           "other than workload",
			totalReplicas:  100,
			objectLimiters: testObjectLimiters,
			sleepDuration:  100 * time.Millisecond,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: ownerReferences1,
				},
			},
			evictedPodsCount: 11,
			evictedWorkload:  &otherOwnerReferences,
			want:             true,
		},
		{
			name:          "disable objectLimiters",
			totalReplicas: 100,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: ownerReferences1,
				},
			},
			evictedPodsCount: 11,
			objectLimiters: config.ObjectLimiterMap{
				config.MigrationLimitObjectWorkload: config.MigrationObjectLimiter{
					Duration: metav1.Duration{Duration: 0},
				},
			},
			want: true,
		},
		{
			name:          "default limiter",
			totalReplicas: 100,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: ownerReferences1,
				},
			},
			evictedPodsCount: 1,
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			var v1beta2args v1alpha2.MigrationControllerArgs
			v1alpha2.SetDefaults_MigrationControllerArgs(&v1beta2args)
			var args config.MigrationControllerArgs
			err := v1alpha2.Convert_v1alpha2_MigrationControllerArgs_To_config_MigrationControllerArgs(&v1beta2args, &args, nil)
			if err != nil {
				panic(err)
			}
			a := filter{client: fakeClient, args: &args, clock: clock.RealClock{}}

			controllerFinder := &fakeControllerFinder{}
			if tt.objectLimiters != nil {
				a.args.ObjectLimiters = tt.objectLimiters
			}

			a.initObjectLimiters()
			if tt.totalReplicas > 0 {
				controllerFinder.replicas = tt.totalReplicas
			}
			a.controllerFinder = controllerFinder
			if tt.evictedPodsCount > 0 {
				for i := 0; i < tt.evictedPodsCount; i++ {
					pod := tt.pod.DeepCopy()
					if tt.evictedWorkload != nil {
						pod.OwnerReferences = []metav1.OwnerReference{
							*tt.evictedWorkload,
						}
					}
					a.trackEvictedPod(pod)
					if tt.sleepDuration > 0 {
						time.Sleep(tt.sleepDuration)
					}
				}
			}
			got := a.filterLimitedObject(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestArbitratedMap(t *testing.T) {
	f := filter{
		arbitratedPodMigrationJobs: map[types.UID]bool{},
	}
	job := &v1alpha1.PodMigrationJob{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-job",
			UID:       uuid.NewUUID(),
		},
	}
	assert.False(t, f.checkJobPassedArbitration(job.UID))

	f.markJobPassedArbitration(job.UID)
	assert.True(t, f.checkJobPassedArbitration(job.UID))

	f.removeJobPassedArbitration(job.UID)
	assert.False(t, f.checkJobPassedArbitration(job.UID))
}
