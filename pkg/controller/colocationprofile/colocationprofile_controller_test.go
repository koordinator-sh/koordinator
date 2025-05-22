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

package colocationprofile

import (
	"context"
	"flag"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func TestReconciler_Reconcile(t *testing.T) {
	scheme := getTestScheme()
	testProfile := &configv1alpha1.ClusterColocationProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-profile",
		},
		Spec: configv1alpha1.ClusterColocationProfileSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"koordinator-colocation-pod": "true",
				},
			},
			Labels: map[string]string{
				"testLabelA": "valueA",
			},
			Annotations: map[string]string{
				"testAnnotationA": "valueA",
			},
			QoSClass:            string(extension.QoSBE),
			KoordinatorPriority: pointer.Int32(1111),
			Patch: runtime.RawExtension{
				Raw: []byte(`{"metadata":{"labels":{"test-patch-label":"patch-a"},"annotations":{"test-patch-annotation":"patch-b"}}}`),
			},
		},
	}
	testPod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
			Labels: map[string]string{
				"koordinator-colocation-pod": "true",
			},
			ResourceVersion: "1000",
			UID:             "xxx",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-a",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			Overhead: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}
	testUnmatchedPod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-2",
			Labels: map[string]string{
				"foo": "bar",
			},
			ResourceVersion: "2000",
			UID:             "yyy",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-a",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			Overhead: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}
	testUnmatchedPod2 := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-3",
			Labels: map[string]string{
				"koordinator-colocation-pod": "true",
			},
			ResourceVersion: "3000",
			UID:             "zzz",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-a",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			Overhead: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	wantPod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
			Labels: map[string]string{
				"koordinator-colocation-pod": "true",
				"testLabelA":                 "valueA",
				"test-patch-label":           "patch-a",
				extension.LabelPodQoS:        string(extension.QoSBE),
				extension.LabelPodPriority:   "1111",
			},
			Annotations: map[string]string{
				"testAnnotationA":       "valueA",
				"test-patch-annotation": "patch-b",
			},
			ResourceVersion: "1001",
			UID:             "xxx",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-a",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			Overhead: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}
	wantPod1 := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
			Labels: map[string]string{
				"koordinator-colocation-pod": "true",
				"testLabelA":                 "valueA",
				"test-patch-label":           "patch-a",
				extension.LabelPodQoS:        string(extension.QoSBE),
				extension.LabelPodPriority:   "1111",
				extension.LabelSchedulerName: "koordinator-scheduler",
			},
			Annotations: map[string]string{
				"testAnnotationA":       "valueA",
				"test-patch-annotation": "patch-b",
			},
			ResourceVersion: "1002",
			UID:             "xxx",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container-a",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			Overhead: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}
	t.Run("test", func(t *testing.T) {
		r := &Reconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(testPod, testUnmatchedPod, testUnmatchedPod2, testProfile).
				WithIndex(&corev1.Pod{}, "spec.nodeName", func(obj client.Object) []string {
					return []string{obj.(*corev1.Pod).Spec.NodeName}
				}).
				Build(),
			Scheme:         scheme,
			rateLimiter:    rate.NewLimiter(rate.Limit(1), 5),
			podUpdateCache: *gocache.New(2*ReconcileInterval, 5*time.Minute),
		}

		originalFn := randIntnFn
		randIntnFn = func(i int) int {
			return 0
		}
		defer func() {
			randIntnFn = originalFn
		}()

		// first reconcile
		result, err := r.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: testProfile.Name,
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, ctrl.Result{RequeueAfter: ReconcileInterval}, result)
		// pod is updated
		got := &corev1.Pod{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Namespace: testPod.Namespace, Name: testPod.Name}, got)
		assert.NoError(t, err)
		assert.Equal(t, wantPod, got)

		// skip reconcile for cached update
		gotProfile := &configv1alpha1.ClusterColocationProfile{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Name: testProfile.Name}, gotProfile)
		assert.NoError(t, err)
		testProfile1 := gotProfile.DeepCopy()
		testProfile1.Spec.Labels[extension.LabelSchedulerName] = "koordinator-scheduler"
		err = r.Client.Patch(context.TODO(), testProfile1, client.MergeFrom(gotProfile))
		assert.NoError(t, err)
		gotProfile = &configv1alpha1.ClusterColocationProfile{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Name: testProfile.Name}, gotProfile)
		assert.NoError(t, err)
		// fake an update
		r.podUpdateCache.SetDefault(getPodUpdateKey(gotProfile, got), struct{}{})
		result, err = r.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: testProfile.Name,
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, ctrl.Result{RequeueAfter: ReconcileInterval}, result)
		// pod does not change
		got = &corev1.Pod{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Namespace: testPod.Namespace, Name: testPod.Name}, got)
		assert.NoError(t, err)
		assert.Equal(t, wantPod, got)

		// update pod since cache expired
		gotProfile = &configv1alpha1.ClusterColocationProfile{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Name: testProfile.Name}, gotProfile)
		assert.NoError(t, err)
		r.podUpdateCache.Delete(getPodUpdateKey(gotProfile, testPod))
		result, err = r.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: testProfile.Name,
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, ctrl.Result{RequeueAfter: ReconcileInterval}, result)
		// pod is updated
		got = &corev1.Pod{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Namespace: testPod.Namespace, Name: testPod.Name}, got)
		assert.NoError(t, err)
		assert.Equal(t, wantPod1, got)

		// reconcile should be idempotent
		gotProfile = &configv1alpha1.ClusterColocationProfile{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Name: testProfile.Name}, gotProfile)
		assert.NoError(t, err)
		r.podUpdateCache.Delete(getPodUpdateKey(gotProfile, testPod))
		result, err = r.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: testProfile.Name,
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, ctrl.Result{RequeueAfter: ReconcileInterval}, result)
		// pod is updated
		got = &corev1.Pod{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Namespace: testPod.Namespace, Name: testPod.Name}, got)
		assert.NoError(t, err)
		assert.Equal(t, wantPod1, got)

		// skip reconcile for rate limited
		gotProfile = &configv1alpha1.ClusterColocationProfile{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Name: testProfile.Name}, gotProfile)
		assert.NoError(t, err)
		testProfile2 := gotProfile.DeepCopy()
		testProfile2.Spec.Labels[extension.LabelSchedulerName] = "koordinator-scheduler-1"
		err = r.Client.Patch(context.TODO(), testProfile2, client.MergeFrom(gotProfile))
		assert.NoError(t, err)
		gotProfile = &configv1alpha1.ClusterColocationProfile{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Name: testProfile.Name}, gotProfile)
		assert.NoError(t, err)
		r.podUpdateCache.Delete(getPodUpdateKey(gotProfile, testPod))
		r.rateLimiter = rate.NewLimiter(rate.Limit(0), 0)
		result, err = r.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: testProfile.Name,
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, ctrl.Result{RequeueAfter: ReconcileInterval}, result)
		// pod does not change
		got = &corev1.Pod{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Namespace: testPod.Namespace, Name: testPod.Name}, got)
		assert.NoError(t, err)
		assert.Equal(t, wantPod1, got)

		// reconcile for selector match nothing
		gotProfile = &configv1alpha1.ClusterColocationProfile{}
		err = r.Client.Get(context.TODO(), client.ObjectKey{Name: testProfile.Name}, gotProfile)
		assert.NoError(t, err)
		testProfile3 := gotProfile.DeepCopy()
		testProfile3.Spec.Selector = nil
		err = r.Client.Patch(context.TODO(), testProfile3, client.MergeFrom(gotProfile))
		assert.NoError(t, err)
		result, err = r.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: testProfile.Name,
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, ctrl.Result{RequeueAfter: ReconcileInterval}, result)

		// skip reconcile for profile
		err = r.Client.Delete(context.TODO(), testProfile3)
		assert.NoError(t, err)
		result, err = r.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: testProfile.Name,
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})
}

type fakeManager struct {
	manager.Manager
}

func (f *fakeManager) GetClient() client.Client { return nil }

func (f *fakeManager) GetEventRecorderFor(name string) record.EventRecorder { return nil }

func (f *fakeManager) GetScheme() *runtime.Scheme { return runtime.NewScheme() }

func (f *fakeManager) GetControllerOptions() config.Controller { return config.Controller{} }

func TestAdd(t *testing.T) {
	tests := []struct {
		name           string
		featureEnabled bool
		arg            ctrl.Manager
		wantErr        bool
	}{
		{
			name:           "feature disabled",
			featureEnabled: false,
			wantErr:        false,
		},
		{
			name:           "feature enabled but CRD not registered",
			arg:            &fakeManager{},
			featureEnabled: true,
			wantErr:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer utilfeature.SetFeatureGateDuringTest(t, utilfeature.DefaultMutableFeatureGate, features.ColocationProfileController, tt.featureEnabled)()
			gotErr := Add(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}

func TestInitFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.PanicOnError)
	InitFlags(fs)
	err := fs.Parse([]string{})
	assert.NoError(t, err)
}

func getTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = slov1alpha1.AddToScheme(s)
	_ = schedulingv1alpha1.AddToScheme(s)
	_ = configv1alpha1.AddToScheme(s)
	return s
}
