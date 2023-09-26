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
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/fieldindex"
)

func TestSortJobsByPod(t *testing.T) {
	testCases := []struct {
		name         string
		order        []int
		expectedJobs []string
	}{
		{
			"test-1",
			[]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
			[]string{"test-job-0", "test-job-1", "test-job-2", "test-job-3", "test-job-4", "test-job-5", "test-job-6", "test-job-7", "test-job-8", "test-job-9"},
		},
		{
			"test-2",
			[]int{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
			[]string{"test-job-9", "test-job-8", "test-job-7", "test-job-6", "test-job-5", "test-job-4", "test-job-3", "test-job-2", "test-job-1", "test-job-0"},
		},
		{
			"test-3",
			[]int{},
			[]string{},
		},
		{
			"test-4",
			[]int{8, 7, 5, 4, 9, 2, 3, 11},
			[]string{"test-job-5", "test-job-6", "test-job-3", "test-job-2", "test-job-1", "test-job-0", "test-job-4", "test-job-7"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			creationTime := time.Now()
			jobs := make([]*v1alpha1.PodMigrationJob, 0, len(testCase.order))
			podOfJob := map[*v1alpha1.PodMigrationJob]*corev1.Pod{}
			podOrder := map[string]int{}
			for i := 0; i < len(testCase.order); i++ {
				pod := makePod("test-pod-"+strconv.Itoa(i), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime)
				job := makePodMigrationJob("test-job-"+strconv.Itoa(i), creationTime, pod)
				jobs = append(jobs, job)
				podOfJob[job] = pod
				podOrder[pod.Name] = testCase.order[i]
			}

			podSorter := SortJobsByPod(
				func(pods []*corev1.Pod) {
					sort.Slice(pods, func(i, j int) bool {
						return podOrder[pods[i].Name] < podOrder[pods[j].Name]
					})
				})
			podSorter(jobs, podOfJob)
			jobsOrder := make([]string, 0, len(jobs))
			for _, v := range jobs {
				jobsOrder = append(jobsOrder, v.Name)
			}
			assert.Equal(t, testCase.expectedJobs, jobsOrder)
		})
	}
}

func TestSortJobsByCreationTime(t *testing.T) {
	fakeJobs := []*v1alpha1.PodMigrationJob{
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(time.Unix(222222, 0)),
				Namespace:         "default",
				Name:              "test-job-1"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(time.Unix(222222222, 0)),
				Namespace:         "default",
				Name:              "test-job-2",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(time.Unix(111111111, 0)),
				Namespace:         "default",
				Name:              "test-job-3",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(time.Unix(11111, 0)),
				Namespace:         "default",
				Name:              "test-job-4",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(time.Unix(11111, 0)),
				Namespace:         "default",
				Name:              "test-job-5",
			},
		},
	}
	fakeRanks := map[*v1alpha1.PodMigrationJob]int{
		fakeJobs[0]: 2,
		fakeJobs[1]: 0,
		fakeJobs[2]: 1,
		fakeJobs[3]: 3,
		fakeJobs[4]: 4,
	}
	fn := SortJobsByCreationTime()
	fakeJobs = fn(fakeJobs, nil)

	for i, job := range fakeJobs {
		assert.Equal(t, fakeRanks[job], i)
	}
}

func TestSortJobsByMigratingNum(t *testing.T) {
	testCases := []struct {
		name                            string
		podMigrationJobNum              int
		runningPodMigrationJobNum       int
		passedPendingPodMigrationJobNum int
		jobNum                          int

		jobOfPodMigrationJob              map[int]int
		jobOfRunningPodMigrationJob       map[int]int
		jobOfPassedPendingPodMigrationJob map[int]int
		expectIdx                         []int
	}{
		{"test-1", 10, 5, 0, 2,
			map[int]int{4: 0, 8: 0}, map[int]int{3: 0, 1: 1}, map[int]int{},
			[]int{4, 8, 0, 1, 2, 3, 5, 6, 7, 9}},
		{"test-2", 10, 2, 0, 2,
			map[int]int{}, map[int]int{}, map[int]int{},
			[]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{"test-3", 5, 2, 0, 2,
			map[int]int{0: 0, 1: 0, 2: 0, 3: 0, 4: 0}, map[int]int{0: 0}, map[int]int{},
			[]int{0, 1, 2, 3, 4}},
		{"test-4", 5, 0, 2, 2,
			map[int]int{2: 0, 3: 0, 4: 0}, map[int]int{}, map[int]int{0: 0},
			[]int{2, 3, 4, 0, 1}},
		{"test-5", 6, 2, 2, 2,
			map[int]int{2: 0, 3: 1, 5: 0}, map[int]int{0: 1}, map[int]int{0: 0},
			[]int{2, 3, 5, 0, 1, 4}},
		{"test-6", 10, 5, 0, 2,
			map[int]int{4: 0, 8: 0, 7: 1}, map[int]int{3: 0, 1: 1, 0: 1}, map[int]int{},
			[]int{7, 4, 8, 0, 1, 2, 3, 5, 6, 9}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := newFieldIndexFakeClient(fake.NewClientBuilder().WithScheme(scheme).Build())

			creationTime := time.Now()
			jobs := make([]*batchv1.Job, testCase.jobNum)
			podMigrationJobs := make([]*v1alpha1.PodMigrationJob, 0, testCase.podMigrationJobNum)
			podOfJob := map[*v1alpha1.PodMigrationJob]*corev1.Pod{}

			// create jobs
			for k := 0; k < testCase.jobNum; k++ {
				job := &batchv1.Job{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Job",
						APIVersion: "batch/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-job-" + strconv.Itoa(k),
						Namespace: "default",
						UID:       types.UID("test-job-" + strconv.Itoa(k)),
					},
				}
				jobs[k] = job
				assert.Nil(t, fakeClient.Create(context.TODO(), job))
			}
			// create PodMigrationJobs
			for k := 0; k < testCase.podMigrationJobNum; k++ {
				pod := makePod("test-pod-"+strconv.Itoa(k), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime)
				job := makePodMigrationJob("test-migration-job-"+strconv.Itoa(k), creationTime, pod)
				job.Status = v1alpha1.PodMigrationJobStatus{
					Phase: "",
				}

				if idx, ok := testCase.jobOfPodMigrationJob[k]; ok {
					controller := true
					pod.SetOwnerReferences([]metav1.OwnerReference{
						{
							APIVersion:         jobs[idx].APIVersion,
							Kind:               jobs[idx].Kind,
							Name:               jobs[idx].Name,
							UID:                jobs[idx].UID,
							Controller:         &controller,
							BlockOwnerDeletion: nil,
						},
					})
				}
				assert.Nil(t, fakeClient.Create(context.TODO(), pod))
				assert.Nil(t, fakeClient.Create(context.TODO(), job))
				podOfJob[job] = pod
				podMigrationJobs = append(podMigrationJobs, job)
			}

			// create Running PodMigrationJobs
			for k := 0; k < testCase.runningPodMigrationJobNum; k++ {
				pod := makePod("test-running-pod-"+strconv.Itoa(k), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime)
				job := makePodMigrationJob("test-running-migration-job-"+strconv.Itoa(k), creationTime, pod)

				job.Status = v1alpha1.PodMigrationJobStatus{
					Phase: v1alpha1.PodMigrationJobRunning,
				}

				if idx, ok := testCase.jobOfRunningPodMigrationJob[k]; ok {
					controller := true
					pod.SetOwnerReferences([]metav1.OwnerReference{
						{
							APIVersion:         jobs[idx].APIVersion,
							Kind:               jobs[idx].Kind,
							Name:               jobs[idx].Name,
							UID:                jobs[idx].UID,
							Controller:         &controller,
							BlockOwnerDeletion: nil,
						},
					})
				}
				assert.Nil(t, fakeClient.Create(context.TODO(), pod))
				assert.Nil(t, fakeClient.Create(context.TODO(), job))
			}

			// create Passed Pending PodMigrationJobs
			for k := 0; k < testCase.passedPendingPodMigrationJobNum; k++ {
				pod := makePod("test-passed-pod-"+strconv.Itoa(k), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime)
				job := makePodMigrationJob("test-passed-migration-job-"+strconv.Itoa(k), creationTime, pod)
				job.Annotations = map[string]string{AnnotationPassedArbitration: "true"}

				if idx, ok := testCase.jobOfPassedPendingPodMigrationJob[k]; ok {
					controller := true
					pod.SetOwnerReferences([]metav1.OwnerReference{
						{
							APIVersion:         jobs[idx].APIVersion,
							Kind:               jobs[idx].Kind,
							Name:               jobs[idx].Name,
							UID:                jobs[idx].UID,
							Controller:         &controller,
							BlockOwnerDeletion: nil,
						},
					})
				}
				assert.Nil(t, fakeClient.Create(context.TODO(), pod))
				assert.Nil(t, fakeClient.Create(context.TODO(), job))
			}

			sortFn := SortJobsByMigratingNum(fakeClient)
			actualPodMigrationJobs := make([]*v1alpha1.PodMigrationJob, len(podMigrationJobs))
			copy(actualPodMigrationJobs, podMigrationJobs)
			sortFn(actualPodMigrationJobs, podOfJob)

			actual := make([]string, 0, len(actualPodMigrationJobs))
			expected := make([]string, 0, len(actualPodMigrationJobs))
			for i, job := range actualPodMigrationJobs {
				actual = append(actual, job.Name)
				expected = append(expected, podMigrationJobs[testCase.expectIdx[i]].Name)
			}
			assert.Equal(t, expected, actual)
		})
	}
}

func TestSortJobsByController(t *testing.T) {
	testCases := []struct {
		name               string
		podMigrationJobNum int
		jobNum             int

		jobOfPodMigrationJob map[int]int
		expectIdx            []int
	}{
		{"test-1", 10, 2,
			map[int]int{4: 0, 8: 0},
			[]int{0, 1, 2, 3, 4, 8, 5, 6, 7, 9}},
		{"test-2", 10, 2,
			map[int]int{},
			[]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{"test-3", 10, 2,
			map[int]int{3: 0, 8: 0, 1: 1, 9: 1},
			[]int{0, 1, 9, 2, 3, 8, 4, 5, 6, 7}},
		{"test-4", 5, 2,
			map[int]int{2: 0, 3: 0, 4: 0},
			[]int{0, 1, 2, 3, 4}},
		{"test-5", 6, 3,
			map[int]int{0: 0, 4: 1, 2: 2},
			[]int{0, 1, 2, 3, 4, 5}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := newFieldIndexFakeClient(fake.NewClientBuilder().WithScheme(scheme).Build())

			creationTime := time.Now()
			jobs := make([]*batchv1.Job, testCase.jobNum)
			podMigrationJobs := make([]*v1alpha1.PodMigrationJob, 0, testCase.podMigrationJobNum)
			podOfJob := map[*v1alpha1.PodMigrationJob]*corev1.Pod{}

			// create jobs
			for k := 0; k < testCase.jobNum; k++ {
				job := &batchv1.Job{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Job",
						APIVersion: "batch/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-job-" + strconv.Itoa(k),
						Namespace: "default",
						UID:       types.UID("test-job-" + strconv.Itoa(k)),
					},
				}
				jobs[k] = job
				assert.Nil(t, fakeClient.Create(context.TODO(), job))
			}
			// create PodMigrationJobs
			for k := 0; k < testCase.podMigrationJobNum; k++ {
				pod := makePod("test-pod-"+strconv.Itoa(k), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime)
				job := makePodMigrationJob("test-migration-job-"+strconv.Itoa(k), creationTime, pod)
				job.Status = v1alpha1.PodMigrationJobStatus{
					Phase: "",
				}

				if idx, ok := testCase.jobOfPodMigrationJob[k]; ok {
					controller := true
					pod.SetOwnerReferences([]metav1.OwnerReference{
						{
							APIVersion:         jobs[idx].APIVersion,
							Kind:               jobs[idx].Kind,
							Name:               jobs[idx].Name,
							UID:                jobs[idx].UID,
							Controller:         &controller,
							BlockOwnerDeletion: nil,
						},
					})
				}
				assert.Nil(t, fakeClient.Create(context.TODO(), pod))
				assert.Nil(t, fakeClient.Create(context.TODO(), job))
				podOfJob[job] = pod
				podMigrationJobs = append(podMigrationJobs, job)
			}

			sortFn := SortJobsByController()
			actualPodMigrationJobs := make([]*v1alpha1.PodMigrationJob, len(podMigrationJobs))
			copy(actualPodMigrationJobs, podMigrationJobs)
			sortFn(actualPodMigrationJobs, podOfJob)

			actual := make([]string, 0, len(actualPodMigrationJobs))
			expected := make([]string, 0, len(actualPodMigrationJobs))
			for i, job := range actualPodMigrationJobs {
				actual = append(actual, job.Name)
				expected = append(expected, podMigrationJobs[testCase.expectIdx[i]].Name)
			}
			assert.Equal(t, expected, actual)
		})
	}
}

func TestGetMigratingJobNum(t *testing.T) {
	testCases := []struct {
		name                            string
		podMigrationJobNum              int
		runningPodMigrationJobNum       int
		passedPendingPodMigrationJobNum int

		expectNum int
	}{
		{"test-1", 3, 3, 3, 6},
		{"test-2", 4, 0, 0, 0},
		{"test-3", 1, 2, 3, 5},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			fakeClient := newFieldIndexFakeClient(fake.NewClientBuilder().WithScheme(scheme).Build())
			creationTime := time.Now()

			// create job
			job := &batchv1.Job{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Job",
					APIVersion: "batch/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-job",
					Namespace: "default",
					UID:       types.UID("test-job"),
				},
			}
			assert.Nil(t, fakeClient.Create(context.TODO(), job))

			owner := []metav1.OwnerReference{
				{
					APIVersion:         job.APIVersion,
					Kind:               job.Kind,
					Name:               job.Name,
					UID:                job.UID,
					Controller:         pointer.Bool(true),
					BlockOwnerDeletion: nil,
				},
			}
			// create PodMigrationJobs
			for k := 0; k < testCase.podMigrationJobNum; k++ {
				pod := makePod("test-pod-"+strconv.Itoa(k), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime, func(pod *corev1.Pod) {
					pod.SetOwnerReferences(owner)
				})
				job := makePodMigrationJob("test-migration-job-"+strconv.Itoa(k), creationTime, pod)
				job.Status = v1alpha1.PodMigrationJobStatus{
					Phase: "",
				}
				assert.Nil(t, fakeClient.Create(context.TODO(), pod))
				assert.Nil(t, fakeClient.Create(context.TODO(), job))
			}

			// create Running PodMigrationJobs
			for k := 0; k < testCase.runningPodMigrationJobNum; k++ {
				pod := makePod("test-running-pod-"+strconv.Itoa(k), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime, func(pod *corev1.Pod) {
					pod.SetOwnerReferences(owner)
				})
				job := makePodMigrationJob("test-running-migration-job-"+strconv.Itoa(k), creationTime, pod)

				job.Status = v1alpha1.PodMigrationJobStatus{
					Phase: v1alpha1.PodMigrationJobRunning,
				}

				assert.Nil(t, fakeClient.Create(context.TODO(), pod))
				assert.Nil(t, fakeClient.Create(context.TODO(), job))
			}

			// create Passed Pending PodMigrationJobs
			for k := 0; k < testCase.passedPendingPodMigrationJobNum; k++ {
				pod := makePod("test-passed-pod-"+strconv.Itoa(k), 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime, func(pod *corev1.Pod) {
					pod.SetOwnerReferences(owner)
				})
				job := makePodMigrationJob("test-passed-migration-job-"+strconv.Itoa(k), creationTime, pod)
				job.Annotations = map[string]string{AnnotationPassedArbitration: "true"}

				assert.Nil(t, fakeClient.Create(context.TODO(), pod))
				assert.Nil(t, fakeClient.Create(context.TODO(), job))
			}
			assert.Equal(t, testCase.expectNum, getMigratingJobNum(fakeClient, job.UID))
		})
	}
}

type fieldIndexFakeClient struct {
	c client.Client
	m map[string]func(obj client.Object) []string
}

func newFieldIndexFakeClient(c client.Client) *fieldIndexFakeClient {
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
