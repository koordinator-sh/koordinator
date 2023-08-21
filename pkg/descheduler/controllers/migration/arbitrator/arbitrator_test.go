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
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/utils/sorter"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSort(t *testing.T) {
	creationTime := time.Now()
	pods := []*corev1.Pod{
		makePod("test-1", 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime),
		makePod("test-10", 0, extension.QoSNone, corev1.PodQOSGuaranteed, creationTime),
		makePod("test-11", 0, extension.QoSNone, corev1.PodQOSBurstable, creationTime),
		makePod("test-12", 8, extension.QoSNone, corev1.PodQOSBestEffort, creationTime),
		makePod("test-13", 9, extension.QoSNone, corev1.PodQOSGuaranteed, creationTime),
		makePod("test-5", extension.PriorityProdValueMax, extension.QoSLSE, corev1.PodQOSGuaranteed, creationTime),
		makePod("test-6", extension.PriorityProdValueMax-1, extension.QoSLSE, corev1.PodQOSGuaranteed, creationTime),
		makePod("test-3", extension.PriorityProdValueMax-100, extension.QoSLS, corev1.PodQOSBurstable, creationTime),
		makePod("test-9", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBurstable, creationTime),
		makePod("test-2", extension.PriorityProdValueMin, extension.QoSLS, corev1.PodQOSBurstable, creationTime),
		makePod("test-8", extension.PriorityBatchValueMax, extension.QoSBE, corev1.PodQOSBurstable, creationTime),
		makePod("test-15", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime),
		makePod("test-16", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime.Add(1*time.Minute)),
		makePod("test-20", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime, withCost(corev1.PodDeletionCost, 200)),
		makePod("test-21", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime, withCost(corev1.PodDeletionCost, 100)),
		makePod("test-22", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime, withCost(corev1.PodDeletionCost, 200), withCost(extension.AnnotationEvictionCost, 200)),
		makePod("test-23", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime, withCost(corev1.PodDeletionCost, 200), withCost(extension.AnnotationEvictionCost, 100)),
		makePod("test-4", extension.PriorityProdValueMax-80, extension.QoSLSR, corev1.PodQOSGuaranteed, creationTime),
		makePod("test-7", extension.PriorityProdValueMax-100, extension.QoSLSR, corev1.PodQOSGuaranteed, creationTime),
	}
	jobs := []*v1alpha1.PodMigrationJob{
		makePodMigrationJob("test-1", creationTime, pods[0]),
		makePodMigrationJob("test-10", creationTime, pods[1]),
		makePodMigrationJob("test-11", creationTime, pods[2]),
		makePodMigrationJob("test-12", creationTime, pods[3]),
		makePodMigrationJob("test-13", creationTime, pods[4]),
		makePodMigrationJob("test-5", creationTime, pods[5]),
		makePodMigrationJob("test-6", creationTime, pods[6]),
		makePodMigrationJob("test-3", creationTime, pods[7]),
		makePodMigrationJob("test-9", creationTime, pods[8]),
		makePodMigrationJob("test-2", creationTime, pods[9]),
		makePodMigrationJob("test-8", creationTime, pods[10]),
		makePodMigrationJob("test-15", creationTime, pods[11]),
		makePodMigrationJob("test-16", creationTime, pods[12]),
		makePodMigrationJob("test-20", creationTime, pods[13]),
		makePodMigrationJob("test-21", creationTime, pods[14]),
		makePodMigrationJob("test-22", creationTime, pods[15]),
		makePodMigrationJob("test-23", creationTime, pods[16]),
		makePodMigrationJob("test-4", creationTime, pods[17]),
		makePodMigrationJob("test-7", creationTime, pods[18]),
	}
	podOfJob := map[*v1alpha1.PodMigrationJob]*corev1.Pod{}
	for i := 0; i < len(pods); i++ {
		podOfJob[jobs[i]] = pods[i]
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	for _, pod := range pods {
		assert.Nil(t, fakeClient.Create(context.TODO(), pod))
	}
	collection := map[types.UID]*v1alpha1.PodMigrationJob{}
	for _, job := range collection {
		collection[job.UID] = job
	}
	arbitrator := &DefaultArbitrator{
		waitCollection: collection,
		client:         fakeClient,
		sorts: []SortFn{
			NewPodSortFn(sorter.PodSorter()),
			NewDisperseByWorkloadSortFn(),
			NewJobGatherSortFn(),
			NewJobMigratingSortFn(fakeClient),
		},
	}
	arbitrator.Sort(jobs, podOfJob)
	expectedPodsOrder := []string{"test-1", "test-12", "test-16", "test-15", "test-21", "test-20", "test-23", "test-22", "test-9", "test-8", "test-11", "test-10", "test-13", "test-2", "test-3", "test-7", "test-4", "test-6", "test-5"}
	var podsOrder []string
	for _, v := range jobs {
		podsOrder = append(podsOrder, v.Name)
	}
	assert.Equal(t, expectedPodsOrder, podsOrder)
}

func TestFilter(t *testing.T) {
	testCases := []struct {
		name            string
		jobNum          int
		nonRetryableMap map[int]bool
		retryableMap    map[int]bool

		expectWaitCollection map[int]bool
		expectQueue          map[int]bool
	}{
		{
			name:            "test-1",
			jobNum:          10,
			nonRetryableMap: map[int]bool{2: true},
			retryableMap:    map[int]bool{3: true, 7: true},

			expectWaitCollection: map[int]bool{3: true, 7: true},
			expectQueue:          map[int]bool{0: true, 1: true, 4: true, 5: true, 6: true, 8: true, 9: true},
		},
		{
			name:            "test-2",
			jobNum:          10,
			nonRetryableMap: map[int]bool{3: true},
			retryableMap:    map[int]bool{3: true, 7: true},

			expectWaitCollection: map[int]bool{7: true},
			expectQueue:          map[int]bool{0: true, 1: true, 2: true, 4: true, 5: true, 6: true, 8: true, 9: true},
		},
		{
			name:            "test-3",
			jobNum:          10,
			nonRetryableMap: map[int]bool{},
			retryableMap:    map[int]bool{},

			expectWaitCollection: map[int]bool{},
			expectQueue:          map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true},
		},
		{
			name:            "test-4",
			jobNum:          5,
			nonRetryableMap: map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true},
			retryableMap:    map[int]bool{},

			expectWaitCollection: map[int]bool{},
			expectQueue:          map[int]bool{},
		},
		{
			name:            "test-5",
			jobNum:          5,
			nonRetryableMap: map[int]bool{},
			retryableMap:    map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true},

			expectWaitCollection: map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true},
			expectQueue:          map[int]bool{},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			_ = appsv1alpha1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			jobs := make([]*v1alpha1.PodMigrationJob, testCase.jobNum)
			podOfJob := map[*v1alpha1.PodMigrationJob]*corev1.Pod{}
			nonRetryablePods := map[*corev1.Pod]bool{}
			var nonRetryableJobs []*v1alpha1.PodMigrationJob
			retryablePods := map[*corev1.Pod]bool{}
			var expectWorkQueueJob []*v1alpha1.PodMigrationJob
			var expectWorkQueue []string
			collection := map[types.UID]*v1alpha1.PodMigrationJob{}
			var expectWaitCollection []types.UID

			for i := range jobs {
				pod := makePod("test-pod-"+strconv.Itoa(i), 0, extension.QoSNone, corev1.PodQOSBestEffort, time.Now())
				jobs[i] = makePodMigrationJob("test-job-"+strconv.Itoa(i), time.Now(), pod)
				assert.Nil(t, fakeClient.Create(context.TODO(), pod))
				assert.Nil(t, fakeClient.Create(context.TODO(), jobs[i]))
				podOfJob[jobs[i]] = pod
				if testCase.nonRetryableMap[i] {
					nonRetryablePods[pod] = true
					nonRetryableJobs = append(nonRetryableJobs, jobs[i])
				}
				if testCase.retryableMap[i] {
					retryablePods[pod] = true
				}
				if testCase.expectWaitCollection[i] {
					expectWaitCollection = append(expectWaitCollection, jobs[i].UID)
				}
				if testCase.expectQueue[i] {
					expectWorkQueueJob = append(expectWorkQueueJob, jobs[i])
					expectWorkQueue = append(expectWorkQueue, jobs[i].Name)
				}
				collection[jobs[i].UID] = jobs[i]
			}
			a := &DefaultArbitrator{
				waitCollection: collection,
				workQueue:      workqueue.NewRateLimitingQueue(&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(100, 100)}),
				nonRetryablePodFilter: func(pod *corev1.Pod) bool {
					return !nonRetryablePods[pod]
				},
				retryablePodFilter: func(pod *corev1.Pod) bool {
					return !retryablePods[pod]
				},
				client:              fakeClient,
				mu:                  sync.Mutex{},
				eventRecorder:       fakeEventRecord{},
				arbitrationInterval: 0,
			}

			a.Filter(jobs, podOfJob)

			var actualWaitCollection []types.UID
			for uid := range a.waitCollection {
				actualWaitCollection = append(actualWaitCollection, uid)
			}
			assert.ElementsMatchf(t, actualWaitCollection, expectWaitCollection, "waitCollection")

			var actualWorkQueue []string
			cnt := 0
			for {
				if cnt >= len(expectWorkQueueJob) {
					break
				}
				item, _ := a.workQueue.Get()
				actualWorkQueue = append(actualWorkQueue, item.(reconcile.Request).Name)
				cnt++
			}
			assert.ElementsMatchf(t, actualWorkQueue, expectWorkQueue, "workQueue")

			for _, job := range nonRetryableJobs {
				assert.Nil(t, fakeClient.Get(context.TODO(), types.NamespacedName{
					Namespace: job.Namespace,
					Name:      job.Name,
				}, job))
				assert.Equal(t, v1alpha1.PodMigrationJobFailed, job.Status.Phase)
			}

			for _, job := range expectWorkQueueJob {
				assert.Nil(t, fakeClient.Get(context.TODO(), types.NamespacedName{
					Namespace: job.Namespace,
					Name:      job.Name,
				}, job))
				assert.Equal(t, "true", job.Annotations[AnnotationPassedArbitration])
			}
		})
	}
}

func TestAddJob(t *testing.T) {
	creationTime := time.Now()
	migratingJobs := []*v1alpha1.PodMigrationJob{
		makePodMigrationJob("test-job-1", creationTime, nil),
		makePodMigrationJob("test-job-2", creationTime, nil),
		makePodMigrationJob("test-job-3", creationTime, nil),
		makePodMigrationJob("test-job-4", creationTime, nil),
		makePodMigrationJob("test-job-5", creationTime, nil),
	}
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	arbitrator := &DefaultArbitrator{
		waitCollection: map[types.UID]*v1alpha1.PodMigrationJob{},
		workQueue:      workqueue.NewRateLimitingQueue(&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(1, 1)}),
		client:         fakeClient,
	}

	for _, job := range migratingJobs {
		arbitrator.AddJob(job)
	}

	var actualJobs []string
	for _, job := range arbitrator.waitCollection {
		actualJobs = append(actualJobs, job.Name)
	}
	expectedJobs := []string{"test-job-1", "test-job-2", "test-job-3", "test-job-4", "test-job-5"}
	assert.ElementsMatchf(t, expectedJobs, actualJobs, "failed")
}

func TestRequeueJobIfRetryablePodFilterFailed(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	job := &v1alpha1.PodMigrationJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test",
			CreationTimestamp: metav1.Time{Time: time.Now()},
		},
		Spec: v1alpha1.PodMigrationJobSpec{
			PodRef: &corev1.ObjectReference{
				Namespace: "default",
				Name:      "test-pod",
			},
		},
	}
	assert.Nil(t, fakeClient.Create(context.TODO(), job))
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Controller: pointer.Bool(true),
					Kind:       "StatefulSet",
					Name:       "test",
					UID:        "2f96233d-a6b9-4981-b594-7c90c987aed9",
				},
			},
		},
		Spec: corev1.PodSpec{
			SchedulerName: "koord-scheduler",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	assert.Nil(t, fakeClient.Create(context.TODO(), pod))
	enter := false

	a := &DefaultArbitrator{
		waitCollection: map[types.UID]*v1alpha1.PodMigrationJob{job.UID: job},
		workQueue:      workqueue.NewRateLimitingQueue(&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(100, 100)}),
		sorts: []SortFn{func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
			return jobs
		}},
		nonRetryablePodFilter: func(pod *corev1.Pod) bool {
			return true
		},
		retryablePodFilter: func(pod *corev1.Pod) bool {
			enter = true
			return false
		},
		client:              fakeClient,
		mu:                  sync.Mutex{},
		eventRecorder:       fakeEventRecord{},
		arbitrationInterval: 0,
	}

	a.doOnceArbitrate()

	assert.True(t, enter)
	assert.Equal(t, 1, len(a.waitCollection))
	assert.NoError(t, fakeClient.Get(context.TODO(), types.NamespacedName{Name: job.Name}, job))
	assert.Equal(t, v1alpha1.PodMigrationJobPhase(""), job.Status.Phase)
	assert.Equal(t, 0, len(job.Annotations))
	assert.Equal(t, "", job.Status.Reason)
}

func TestAbortJobIfNonRetryablePodFilterFailed(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	job := &v1alpha1.PodMigrationJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test",
			CreationTimestamp: metav1.Time{Time: time.Now()},
		},
		Spec: v1alpha1.PodMigrationJobSpec{
			PodRef: &corev1.ObjectReference{
				Namespace: "default",
				Name:      "test-pod",
			},
		},
	}
	assert.Nil(t, fakeClient.Create(context.TODO(), job))
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Controller: pointer.Bool(true),
					Kind:       "StatefulSet",
					Name:       "test",
					UID:        "2f96233d-a6b9-4981-b594-7c90c987aed9",
				},
			},
		},
		Spec: corev1.PodSpec{
			SchedulerName: "koord-scheduler",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	assert.Nil(t, fakeClient.Create(context.TODO(), pod))
	enter := false

	a := &DefaultArbitrator{
		waitCollection: map[types.UID]*v1alpha1.PodMigrationJob{job.UID: job},
		workQueue:      workqueue.NewRateLimitingQueue(&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(100, 100)}),
		sorts: []SortFn{func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
			return jobs
		}},
		nonRetryablePodFilter: func(pod *corev1.Pod) bool {
			enter = true
			return false
		},
		retryablePodFilter: func(pod *corev1.Pod) bool {
			return true
		},
		client:              fakeClient,
		mu:                  sync.Mutex{},
		eventRecorder:       fakeEventRecord{},
		arbitrationInterval: 0,
	}

	a.doOnceArbitrate()

	assert.True(t, enter)

	assert.NoError(t, fakeClient.Get(context.TODO(), types.NamespacedName{Name: job.Name}, job))
	assert.Equal(t, v1alpha1.PodMigrationJobFailed, job.Status.Phase)
	assert.Equal(t, v1alpha1.PodMigrationJobReasonForbiddenMigratePod, job.Status.Reason)
}

func TestDoOnceArbitrate(t *testing.T) {
	testCases := []struct {
		name            string
		jobNum          int
		nonRetryableMap map[int]bool
		retryableMap    map[int]bool
		order           map[int]int

		expectWaitCollection map[int]bool
		expectQueue          map[int]int
	}{
		{
			name:            "test-1",
			jobNum:          10,
			nonRetryableMap: map[int]bool{2: true},
			retryableMap:    map[int]bool{3: true, 7: true},
			order:           map[int]int{0: 0, 1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6, 7: 7, 8: 8, 9: 9},

			expectWaitCollection: map[int]bool{3: true, 7: true},
			expectQueue:          map[int]int{0: 1, 1: 2, 4: 3, 5: 4, 6: 5, 8: 6, 9: 7},
		},
		{
			name:            "test-2",
			jobNum:          10,
			nonRetryableMap: map[int]bool{3: true},
			retryableMap:    map[int]bool{3: true, 7: true},
			order:           map[int]int{0: 9, 1: 8, 2: 7, 3: 6, 4: 5, 5: 4, 6: 3, 7: 2, 8: 1, 9: 0},

			expectWaitCollection: map[int]bool{7: true},
			expectQueue:          map[int]int{0: 8, 1: 7, 2: 6, 4: 5, 5: 4, 6: 3, 8: 2, 9: 1},
		},
		{
			name:            "test-3",
			jobNum:          5,
			nonRetryableMap: map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true},
			retryableMap:    map[int]bool{},

			expectWaitCollection: map[int]bool{},
			expectQueue:          map[int]int{},
		},
		{
			name:            "test-4",
			jobNum:          5,
			nonRetryableMap: map[int]bool{},
			retryableMap:    map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true},
			order:           map[int]int{0: 0, 1: 3, 2: 1, 3: 4, 4: 2},

			expectWaitCollection: map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true},
			expectQueue:          map[int]int{},
		},
		{
			name:            "test-5",
			jobNum:          5,
			nonRetryableMap: map[int]bool{},
			retryableMap:    map[int]bool{2: true},
			order:           map[int]int{0: 0, 1: 3, 2: 1, 3: 4, 4: 2},

			expectWaitCollection: map[int]bool{2: true},
			expectQueue:          map[int]int{0: 1, 1: 3, 3: 4, 4: 2},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = clientgoscheme.AddToScheme(scheme)
			_ = appsv1alpha1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			jobs := make([]*v1alpha1.PodMigrationJob, testCase.jobNum)
			podOfJob := map[*v1alpha1.PodMigrationJob]*corev1.Pod{}
			nonRetryablePods := map[string]bool{}
			retryablePods := map[string]bool{}
			expectWorkQueue := map[string]int{}
			collection := map[types.UID]*v1alpha1.PodMigrationJob{}
			var expectWaitCollection []types.UID
			order := map[*v1alpha1.PodMigrationJob]int{}

			for i := range jobs {
				pod := makePod("test-pod-"+strconv.Itoa(i), 0, extension.QoSNone, corev1.PodQOSBestEffort, time.Now())
				jobs[i] = makePodMigrationJob("test-job-"+strconv.Itoa(i), time.Now(), pod)
				assert.Nil(t, fakeClient.Create(context.TODO(), pod))
				assert.Nil(t, fakeClient.Create(context.TODO(), jobs[i]))
				podOfJob[jobs[i]] = pod
				if testCase.nonRetryableMap[i] {
					nonRetryablePods[pod.Name] = true
				}
				if testCase.retryableMap[i] {
					retryablePods[pod.Name] = true
				}
				if testCase.expectWaitCollection[i] {
					expectWaitCollection = append(expectWaitCollection, jobs[i].UID)
				}
				if v, ok := testCase.expectQueue[i]; ok {
					expectWorkQueue[jobs[i].Name] = v
				}
				order[jobs[i]] = testCase.order[i]
				collection[jobs[i].UID] = jobs[i]
			}
			a := &DefaultArbitrator{
				waitCollection: collection,
				workQueue:      workqueue.NewRateLimitingQueue(&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(100, 100)}),
				nonRetryablePodFilter: func(pod *corev1.Pod) bool {
					return !nonRetryablePods[pod.Name]
				},
				retryablePodFilter: func(pod *corev1.Pod) bool {
					return !retryablePods[pod.Name]
				},
				sorts: []SortFn{
					func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
						sort.Slice(jobs, func(i, j int) bool {
							return order[jobs[i]] < order[jobs[j]]
						})
						return jobs
					}},
				client:              fakeClient,
				mu:                  sync.Mutex{},
				eventRecorder:       fakeEventRecord{},
				arbitrationInterval: 0,
			}

			a.doOnceArbitrate()

			var actualWaitCollection []types.UID
			for uid := range a.waitCollection {
				actualWaitCollection = append(actualWaitCollection, uid)
			}
			assert.ElementsMatchf(t, actualWaitCollection, expectWaitCollection, "waitCollection")

			actualWorkQueue := map[string]int{}
			cnt := 0
			for {
				if cnt >= len(expectWorkQueue) {
					break
				}
				cnt++
				item, _ := a.workQueue.Get()
				actualWorkQueue[item.(reconcile.Request).Name] = cnt
			}
			assert.Equal(t, expectWorkQueue, actualWorkQueue, "workQueue")
		})
	}
}

func TestArbitrate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	a := &DefaultArbitrator{
		waitCollection: map[types.UID]*v1alpha1.PodMigrationJob{},
		workQueue:      workqueue.NewRateLimitingQueue(&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(100, 100)}),
		nonRetryablePodFilter: func(pod *corev1.Pod) bool {
			return true
		},
		retryablePodFilter: func(pod *corev1.Pod) bool {
			return true
		},
		sorts: []SortFn{
			func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
				return jobs
			}},
		client:              fakeClient,
		mu:                  sync.Mutex{},
		eventRecorder:       fakeEventRecord{},
		arbitrationInterval: 500,
	}

	ch := make(<-chan struct{})
	go a.Arbitrate(ch)

	for i := 0; i < 5; i++ {
		pod := makePod("test-pod-"+strconv.Itoa(i), 0, extension.QoSNone, corev1.PodQOSBestEffort, time.Now())
		job := makePodMigrationJob("test-job-"+strconv.Itoa(i), time.Now(), pod)
		assert.Nil(t, fakeClient.Create(context.TODO(), pod))
		assert.Nil(t, fakeClient.Create(context.TODO(), job))
		a.AddJob(job)
		time.Sleep(800 * time.Millisecond)
		actualName, _ := a.workQueue.Get()
		assert.Equal(t, job.Name, actualName.(reconcile.Request).Name)
	}
}

func TestUpdatePassedJob(t *testing.T) {
	job := &v1alpha1.PodMigrationJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			UID:       "test-uid",
		},
	}
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build()
	arbitrator := &DefaultArbitrator{
		waitCollection: map[types.UID]*v1alpha1.PodMigrationJob{job.UID: job},
		client:         fakeClient,
		mu:             sync.Mutex{},
		eventRecorder:  fakeEventRecord{},
		workQueue:      workqueue.NewRateLimitingQueue(&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(1, 1)}),
	}
	arbitrator.updatePassedJob(job)

	assert.Equal(t, 0, len(arbitrator.waitCollection))

	actualJob := &v1alpha1.PodMigrationJob{}
	assert.Nil(t, fakeClient.Get(context.TODO(), types.NamespacedName{
		Namespace: "default",
		Name:      "test",
	}, actualJob))
	assert.Equal(t, map[string]string{AnnotationPassedArbitration: "true"}, actualJob.Annotations)
}

func TestUpdateNonRetryableJobFailed(t *testing.T) {
	job := &v1alpha1.PodMigrationJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			UID:       "test-uid",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build()
	arbitrator := &DefaultArbitrator{
		waitCollection: map[types.UID]*v1alpha1.PodMigrationJob{job.UID: job},
		client:         fakeClient,
		mu:             sync.Mutex{},
		eventRecorder:  fakeEventRecord{},
	}
	arbitrator.updateNonRetryableFailedJob(job, pod)

	assert.Equal(t, 0, len(arbitrator.waitCollection))

	actualJob := &v1alpha1.PodMigrationJob{}
	assert.Nil(t, fakeClient.Get(context.TODO(), types.NamespacedName{
		Namespace: "default",
		Name:      "test",
	}, actualJob))
	assert.Equal(t, v1alpha1.PodMigrationJobFailed, actualJob.Status.Phase)
}

type fakeEventRecord struct{}

func (f fakeEventRecord) Eventf(regarding runtime.Object, related runtime.Object, eventtype, reason, action, note string, args ...interface{}) {
}
