/*
Copyright 2023 The Koordinator Authors.

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
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/fieldindex"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/storageos/go-api/types"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNamespaceGroupFilter(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := NewFieldIndexFakeClient(fake.NewClientBuilder().WithScheme(scheme).Build())

	namespaceOfJobs := map[int]string{
		0: types.DefaultNamespace,
		1: "namespace1",
		2: "namespace2",
		3: "namespace2",
		4: "namespace1",
		5: "",
		6: types.DefaultNamespace,
		7: "",
		8: types.DefaultNamespace,
		9: "namespace2",
	}

	pods := make([]*corev1.Pod, 10)
	podMigrationJobs := make([]*v1alpha1.PodMigrationJob, 10)

	for i := 0; i < len(pods); i++ {
		pods[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceOfJobs[i],
				Name:      "test-pod-" + strconv.Itoa(i),
			},
		}

		podMigrationJobs[i] = &v1alpha1.PodMigrationJob{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceOfJobs[i],
				Name:      "test-job-" + strconv.Itoa(i),
			},
			Spec: v1alpha1.PodMigrationJobSpec{
				PodRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Namespace: namespaceOfJobs[i],
					Name:      pods[i].Name,
				},
			},
		}
	}

	expectedJobs := map[*v1alpha1.PodMigrationJob]struct{}{
		podMigrationJobs[0]: {},
		podMigrationJobs[1]: {},
		podMigrationJobs[2]: {},
		podMigrationJobs[3]: {},
		podMigrationJobs[4]: {},
		podMigrationJobs[5]: {},
		podMigrationJobs[6]: {},
		podMigrationJobs[9]: {},
	}
	// create pods
	for _, pod := range pods {
		err := fakeClient.Create(ctx, pod)
		if err != nil {
			t.Errorf("failed to create pod %v", pod)
			return
		}
	}

	// create GroupFilterFn
	f := NewNamespaceGroupFilter(fakeClient, 3)
	actualJobs := f(podMigrationJobs)

	assert.Equal(t, len(expectedJobs), len(actualJobs), "The number of expected Jobs is not equal to the number of actual Jobs.")
	for _, job := range actualJobs {
		if _, ok := expectedJobs[job]; !ok {
			assert.Fail(t, "This job was not filtered out.")
		}
	}
}

func TestNodeGroupFilter(t *testing.T) {
	testCases := []struct {
		maxJobsPerNode            int
		nodeOfArbitratingJobPod   map[int]int
		nodeOfRunningJobPod       map[int]int
		nodeOfPassedPendingJobPod map[int]int

		expectedIdx []int
	}{
		{4,
			map[int]int{
				0: 0,
				1: 0,
				2: 0,
				3: 1,
				4: 1,
				5: 1,
				6: 1,
				7: 2,
				8: 2,
				9: 2,
			},
			map[int]int{
				0: 1,
			},
			map[int]int{
				0: 0,
			},
			[]int{0, 1, 2, 3, 4, 5, 7, 8, 9}},
	}

	for _, testCase := range testCases {
		scheme := runtime.NewScheme()
		_ = v1alpha1.AddToScheme(scheme)
		_ = clientgoscheme.AddToScheme(scheme)
		_ = appsv1alpha1.AddToScheme(scheme)
		fakeClient := NewFieldIndexFakeClient(fake.NewClientBuilder().WithScheme(scheme).Build())
		// create arbitrating jobs
		arbitratingJobs := make([]*v1alpha1.PodMigrationJob, len(testCase.nodeOfArbitratingJobPod))
		creationTime := time.Now()
		var pods []*corev1.Pod
		for i := 0; i < len(testCase.nodeOfArbitratingJobPod); i++ {
			pod := makePod("test-arbitrating-pod-"+strconv.Itoa(i), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime, func(pod *corev1.Pod) {
				pod.Spec.NodeName = "node-" + strconv.Itoa(testCase.nodeOfArbitratingJobPod[i])
			})
			pods = append(pods, pod)
			arbitratingJobs[i] = makePodMigrationJob("test-arbitrating-job-"+strconv.Itoa(i), creationTime, pod)
			err := fakeClient.Create(context.TODO(), pod)
			if err != nil {
				t.Errorf("failed to create pod %v, err: %v", pod.Name, err)
				return
			}

			err = fakeClient.Create(context.TODO(), arbitratingJobs[i])
			if err != nil {
				t.Errorf("failed to create PodMigrationJob %v, err: %v", arbitratingJobs[i].Name, err)
				return
			}
		}

		// create running jobs
		runningJobs := make([]*v1alpha1.PodMigrationJob, len(testCase.nodeOfRunningJobPod))
		for i := 0; i < len(testCase.nodeOfRunningJobPod); i++ {
			pod := makePod("test-running-pod-"+strconv.Itoa(i), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime, func(pod *corev1.Pod) {
				pod.Spec.NodeName = "node-" + strconv.Itoa(testCase.nodeOfRunningJobPod[i])
			})
			pods = append(pods, pod)
			runningJobs[i] = makePodMigrationJob("test-running-job-"+strconv.Itoa(i), creationTime, pod)
			runningJobs[i].Status.Phase = v1alpha1.PodMigrationJobRunning

			err := fakeClient.Create(context.TODO(), pod)
			if err != nil {
				t.Errorf("failed to create pod %v, err: %v", pod.Name, err)
				return
			}
			err = fakeClient.Create(context.TODO(), runningJobs[i])
			if err != nil {
				t.Errorf("failed to create PodMigrationJob %v, err: %v", runningJobs[i].Name, err)
				return
			}
		}

		// create passed pending jobs
		passedPendingJobs := make([]*v1alpha1.PodMigrationJob, len(testCase.nodeOfPassedPendingJobPod))
		for i := 0; i < len(testCase.nodeOfPassedPendingJobPod); i++ {
			pod := makePod("test-passedPendingJob-pod-"+strconv.Itoa(i), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime, func(pod *corev1.Pod) {
				pod.Spec.NodeName = "node-" + strconv.Itoa(testCase.nodeOfPassedPendingJobPod[i])
			})
			pods = append(pods, pod)
			passedPendingJobs[i] = makePodMigrationJob("test-passedPendingJob-job-"+strconv.Itoa(i), creationTime, pod)
			passedPendingJobs[i].Annotations = map[string]string{AnnotationPassedArbitration: "true"}

			err := fakeClient.Create(context.TODO(), pod)
			if err != nil {
				t.Errorf("failed to create pod %v, err: %v", pod.Name, err)
				return
			}
			err = fakeClient.Create(context.TODO(), passedPendingJobs[i])
			if err != nil {
				t.Errorf("failed to create PodMigrationJob %v, err: %v", passedPendingJobs[i].Name, err)
				return
			}
		}

		// create GroupFilterFn
		f := NewNodeGroupFilter(fakeClient, testCase.maxJobsPerNode)
		actualJobs := make([]*v1alpha1.PodMigrationJob, len(arbitratingJobs))
		copy(actualJobs, arbitratingJobs)
		actualJobs = f(actualJobs)

		expectedJobs := make([]*v1alpha1.PodMigrationJob, len(testCase.expectedIdx))
		for i, idx := range testCase.expectedIdx {
			expectedJobs[i] = arbitratingJobs[idx]
		}

		assert.ElementsMatchf(t, expectedJobs, actualJobs, "failed")
	}
}

func TestWorkloadGroupFilter(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := NewFieldIndexFakeClient(fake.NewClientBuilder().WithScheme(scheme).Build())
	pods := make([]*corev1.Pod, 12)
	podMigrationJobs := make([]*v1alpha1.PodMigrationJob, 12)

	// create rs
	rs := &v1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ReplicaSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rs1",
			Namespace: "defualt",
			UID:       "4fb33a91-7703-4b1d-99db-24ff5d8bbfab",
		},
	}

	// create pods
	for i := 0; i < len(pods); i++ {
		pods[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: types.DefaultNamespace,
				Name:      "test-pod-" + strconv.Itoa(i),
			},
		}

		podMigrationJobs[i] = &v1alpha1.PodMigrationJob{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: types.DefaultNamespace,
				Name:      "test-job-" + strconv.Itoa(i),
			},
			Spec: v1alpha1.PodMigrationJobSpec{
				PodRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Namespace: types.DefaultNamespace,
					Name:      pods[i].Name,
				},
			},
		}
	}

	podMigrationJobs[10].Annotations = map[string]string{AnnotationPassedArbitration: "true"}
	podMigrationJobs[11].Status.Phase = v1alpha1.PodMigrationJobRunning

	pods[1].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(rs)})
	pods[4].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(rs)})
	pods[10].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(rs)})
	pods[11].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(rs)})

	expectedJobs := []string{
		"test-job-0",
		"test-job-2",
		"test-job-3",
		"test-job-5",
		"test-job-6",
		"test-job-7",
		"test-job-8",
		"test-job-9",
	}

	// create rs
	if err := fakeClient.Create(ctx, rs); err != nil {
		t.Errorf("failed to create rs %v", err)
		return
	}

	// create pod and job
	for i := 0; i < len(pods); i++ {
		pod := pods[i]
		if err := fakeClient.Create(ctx, pod); err != nil {
			t.Errorf("failed to create pod %v", err)
			return
		}

		if err := fakeClient.Create(ctx, podMigrationJobs[i]); err != nil {
			t.Errorf("failed to create job %v", err)
			return
		}
	}
	// create GroupFilterFn
	f := NewWorkloadGroupFilter(fakeClient, &fakeControllerFinder{
		pods:     []*corev1.Pod{pods[1], pods[4], pods[10], pods[11]},
		replicas: 4,
		err:      nil,
	}, intstr.IntOrString{Type: intstr.Int, IntVal: 2}, intstr.IntOrString{Type: intstr.Int, IntVal: 1}, true)

	jobs := f(podMigrationJobs[:10])
	var actualJobs []string
	for _, job := range jobs {
		actualJobs = append(actualJobs, job.Name)
	}
	assert.ElementsMatchf(t, actualJobs, expectedJobs, "failed")
}

func TestNewSingleJobGroupFilter(t *testing.T) {
	testCases := []struct {
		migratingPMJNum   int
		jobNum            int
		jobOfMigratingPMJ map[int]int
		expectedIdx       map[int]bool
	}{
		{10, 3,
			map[int]int{1: 0, 2: 1, 3: 2},
			map[int]bool{0: true, 1: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true},
		},
		{10, 3,
			map[int]int{0: 0, 1: 0, 2: 1, 3: 2, 7: 0},
			map[int]bool{2: true, 4: true, 5: true, 6: true, 8: true, 9: true},
		},
		{10, 0,
			map[int]int{},
			map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true},
		},
		{10, 2,
			map[int]int{6: 0},
			map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true},
		},
	}

	for _, testCase := range testCases {
		scheme := runtime.NewScheme()
		_ = v1alpha1.AddToScheme(scheme)
		_ = clientgoscheme.AddToScheme(scheme)
		_ = appsv1alpha1.AddToScheme(scheme)
		creationTime := time.Now()

		// create job
		jobList := batchv1.JobList{}
		for i := 0; i < testCase.jobNum; i++ {
			job := batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-job-" + strconv.Itoa(i),
					UID:       apimachinerytypes.UID("test-job-" + strconv.Itoa(i)),
				},
			}
			jobList.Items = append(jobList.Items, job)
		}

		// create migrating PMJ
		migratingPMJList := v1alpha1.PodMigrationJobList{}
		var migratingPMJs []*v1alpha1.PodMigrationJob
		podList := corev1.PodList{}
		for i := 0; i < testCase.migratingPMJNum; i++ {
			controller := true
			pod := makePod("test-migrating-pod-"+strconv.Itoa(i), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime, func(pod *corev1.Pod) {
				if idx, ok := testCase.jobOfMigratingPMJ[i]; ok {
					pod.OwnerReferences = []metav1.OwnerReference{{
						Kind:               "Job",
						Name:               jobList.Items[idx].Name,
						UID:                jobList.Items[idx].UID,
						Controller:         &controller,
						BlockOwnerDeletion: nil,
					}}
				}
			})
			pmj := makePodMigrationJob("test-migrating-pmj-"+strconv.Itoa(i), creationTime, pod)
			migratingPMJList.Items = append(migratingPMJList.Items, *pmj)
			migratingPMJs = append(migratingPMJs, pmj)
			podList.Items = append(podList.Items, *pod)
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).
			WithLists(&jobList).WithLists(&migratingPMJList).WithLists(&podList).Build()

		f := NewSingleJobGroupFilter(fakeClient)
		actualPMJs := f(migratingPMJs)
		var expectedPMJs []*v1alpha1.PodMigrationJob
		for i, _ := range testCase.expectedIdx {
			expectedPMJs = append(expectedPMJs, migratingPMJs[i])
		}
		assert.ElementsMatchf(t, expectedPMJs, actualPMJs, "failed")
	}
}

type fieldIndexFakeClient struct {
	c client.Client
	m map[string]func(obj client.Object) []string
}

func NewFieldIndexFakeClient(c client.Client) *fieldIndexFakeClient {
	m := map[string]func(obj client.Object) []string{
		fieldindex.IndexPodByNodeName: func(obj client.Object) []string {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return []string{}
			}
			if len(pod.Spec.NodeName) == 0 {
				return []string{}
			}
			return []string{pod.Spec.NodeName}
		},
		fieldindex.IndexPodByOwnerRefUID: func(obj client.Object) []string {
			var owners []string
			for _, ref := range obj.GetOwnerReferences() {
				owners = append(owners, string(ref.UID))
			}
			return owners
		},
		fieldindex.IndexJobByPodUID: func(obj client.Object) []string {
			migrationJob, ok := obj.(*v1alpha1.PodMigrationJob)
			if !ok {
				return []string{}
			}
			if migrationJob.Spec.PodRef == nil {
				return []string{}
			}
			return []string{string(migrationJob.Spec.PodRef.UID)}
		},
		fieldindex.IndexJobPodNamespacedName: func(obj client.Object) []string {
			migrationJob, ok := obj.(*v1alpha1.PodMigrationJob)
			if !ok {
				return []string{}
			}
			if migrationJob.Spec.PodRef == nil {
				return []string{}
			}
			return []string{fmt.Sprintf("%s/%s", migrationJob.Spec.PodRef.Namespace, migrationJob.Spec.PodRef.Name)}
		},
		fieldindex.IndexJobByPodNamespace: func(obj client.Object) []string {
			migrationJob, ok := obj.(*v1alpha1.PodMigrationJob)
			if !ok {
				return []string{}
			}
			if migrationJob.Spec.PodRef == nil {
				return []string{}
			}
			return []string{migrationJob.Spec.PodRef.Namespace}
		},
	}
	return &fieldIndexFakeClient{
		m: m,
		c: c,
	}
}

func (f *fieldIndexFakeClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	return f.c.Get(ctx, key, obj)
}

func (f *fieldIndexFakeClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	err := f.c.List(ctx, list, opts...)
	if err != nil {
		return err
	}
	lo := client.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(&lo)
	}
	if reflect.TypeOf(lo.FieldSelector).String() == "*fields.hasTerm" {
		splits := strings.Split(lo.FieldSelector.String(), "=")
		switch splits[0] {
		case fieldindex.IndexPodByNodeName, fieldindex.IndexPodByOwnerRefUID:
			if reflect.TypeOf(list).String() == "*v1.PodList" {
				items := list.(*corev1.PodList).Items
				var fieldIndexItems []corev1.Pod
				for _, item := range items {
					vs := f.m[splits[0]](&item)
					for _, v := range vs {
						if v == splits[1] {
							fieldIndexItems = append(fieldIndexItems, item)
							break
						}
					}
				}
				list.(*corev1.PodList).Items = fieldIndexItems
			}
		case fieldindex.IndexJobByPodUID, fieldindex.IndexJobByPodNamespace, fieldindex.IndexJobPodNamespacedName:
			if reflect.TypeOf(list).String() == "*v1alpha1.PodMigrationJobList" {
				items := list.(*v1alpha1.PodMigrationJobList).Items
				var fieldIndexItems []v1alpha1.PodMigrationJob
				for _, item := range items {
					vs := f.m[splits[0]](&item)
					for _, v := range vs {
						if v == splits[1] {
							fieldIndexItems = append(fieldIndexItems, item)
							break
						}
					}
				}
				list.(*v1alpha1.PodMigrationJobList).Items = fieldIndexItems
			}
		}
	}
	return nil
}

func (f *fieldIndexFakeClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return f.c.Create(ctx, obj, opts...)
}

func (f *fieldIndexFakeClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return f.c.Delete(ctx, obj, opts...)
}

func (f *fieldIndexFakeClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return f.c.Update(ctx, obj, opts...)
}

func (f *fieldIndexFakeClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return f.c.Patch(ctx, obj, patch, opts...)
}

func (f *fieldIndexFakeClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return f.c.DeleteAllOf(ctx, obj, opts...)
}

func (f *fieldIndexFakeClient) Status() client.StatusWriter {
	return f.c.Status()
}

func (f *fieldIndexFakeClient) Scheme() *runtime.Scheme {
	return f.c.Scheme()
}

func (f *fieldIndexFakeClient) RESTMapper() meta.RESTMapper {
	return f.c.RESTMapper()
}

type fakeControllerFinder struct {
	pods     []*corev1.Pod
	replicas int32
	err      error
}

func (f *fakeControllerFinder) ListPodsByWorkloads(workloadUIDs []apimachinerytypes.UID, ns string, labelSelector *metav1.LabelSelector, active bool) ([]*corev1.Pod, error) {
	return f.pods, f.err
}

func (f *fakeControllerFinder) GetPodsForRef(ownerReference *metav1.OwnerReference, ns string, labelSelector *metav1.LabelSelector, active bool) ([]*corev1.Pod, int32, error) {
	return f.pods, f.replicas, f.err
}

func (f *fakeControllerFinder) GetExpectedScaleForPod(pod *corev1.Pod) (int32, error) {
	return f.replicas, f.err
}
