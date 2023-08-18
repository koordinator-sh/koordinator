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
	apitypes "github.com/storageos/go-api/types"
	"github.com/stretchr/testify/assert"
	v12 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strconv"
	"testing"
	"time"
)

func TestPodSortFn(t *testing.T) {
	creationTime := time.Now()
	pods := []*corev1.Pod{
		makePod("test-1", 0, extension.QoSNone, corev1.PodQOSBestEffort, creationTime),
		makePod("test-10", 0, extension.QoSNone, corev1.PodQOSGuaranteed, creationTime),
		makePod("test-11", 0, extension.QoSNone, corev1.PodQOSBurstable, creationTime),
		makePod("test-12", 8, extension.QoSNone, corev1.PodQOSBestEffort, creationTime),
		makePod("test-13", 9, extension.QoSNone, corev1.PodQOSGuaranteed, creationTime),
		makePod("test-14", 10, extension.QoSNone, corev1.PodQOSBurstable, creationTime),
		makePod("test-5", extension.PriorityProdValueMax, extension.QoSLSE, corev1.PodQOSGuaranteed, creationTime),
		makePod("test-6", extension.PriorityProdValueMax-1, extension.QoSLSE, corev1.PodQOSGuaranteed, creationTime),
		makePod("test-3", extension.PriorityProdValueMax-100, extension.QoSLS, corev1.PodQOSBurstable, creationTime),
		makePod("test-9", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBurstable, creationTime),
		makePod("test-2", extension.PriorityProdValueMin, extension.QoSLS, corev1.PodQOSBurstable, creationTime),
		makePod("test-8", extension.PriorityBatchValueMax, extension.QoSBE, corev1.PodQOSBurstable, creationTime),
		makePod("test-15", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime),
		makePod("test-16", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime.Add(1*time.Minute)),
		makePod("test-17", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime),
		makePod("test-18", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime.Add(1*time.Minute)),
		makePod("test-19", extension.PriorityBatchValueMin, extension.QoSBE, corev1.PodQOSBestEffort, creationTime),
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
		makePodMigrationJob("test-14", creationTime, pods[5]),
		makePodMigrationJob("test-5", creationTime, pods[6]),
		makePodMigrationJob("test-6", creationTime, pods[7]),
		makePodMigrationJob("test-3", creationTime, pods[8]),
		makePodMigrationJob("test-9", creationTime, pods[9]),
		makePodMigrationJob("test-2", creationTime, pods[10]),
		makePodMigrationJob("test-8", creationTime, pods[11]),
		makePodMigrationJob("test-15", creationTime, pods[12]),
		makePodMigrationJob("test-16", creationTime, pods[13]),
		makePodMigrationJob("test-17", creationTime, pods[14]),
		makePodMigrationJob("test-18", creationTime, pods[15]),
		makePodMigrationJob("test-19", creationTime, pods[16]),
		makePodMigrationJob("test-20", creationTime, pods[17]),
		makePodMigrationJob("test-21", creationTime, pods[18]),
		makePodMigrationJob("test-22", creationTime, pods[19]),
		makePodMigrationJob("test-23", creationTime, pods[20]),
		makePodMigrationJob("test-4", creationTime, pods[21]),
		makePodMigrationJob("test-7", creationTime, pods[22]),
	}

	fakeClient := fake.NewClientBuilder().Build()
	for _, pod := range pods {
		err := fakeClient.Create(context.TODO(), pod)
		if err != nil {
			t.Errorf("failed to create pod, err: %v", err)
		}
	}
	podSorter := NewPodSortFn(fakeClient, sorter.PodSorter())
	podSorter(jobs)
	expectedPodsOrder := []string{"test-1", "test-12", "test-18", "test-16", "test-19", "test-17", "test-15", "test-21", "test-20", "test-23", "test-22", "test-9", "test-8", "test-11", "test-10", "test-13", "test-14", "test-2", "test-3", "test-7", "test-4", "test-6", "test-5"}
	var podsOrder []string
	for _, v := range jobs {
		podsOrder = append(podsOrder, v.Name)
	}
	assert.Equal(t, expectedPodsOrder, podsOrder)
}

func TestTimeSortFn(t *testing.T) {
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
				Namespace:         apitypes.DefaultNamespace,
				Name:              "test-job-2",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(time.Unix(111111111, 0)),
				Namespace:         apitypes.DefaultNamespace,
				Name:              "test-job-3",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(time.Unix(11111, 0)),
				Namespace:         apitypes.DefaultNamespace,
				Name:              "test-job-4",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(time.Unix(11111, 0)),
				Namespace:         apitypes.DefaultNamespace,
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
	fn := NewTimeSortFn()
	fakeJobs = fn(fakeJobs)

	for i, job := range fakeJobs {
		assert.Equal(t, fakeRanks[job], i)
	}
}

func TestJobMigratingSortFn(t *testing.T) {
	testCases := []struct {
		podMigrationJobNums        int
		runningPodMigrationJobNums int
		jobNums                    int

		jobOfPodMigrationJob        map[int]int
		jobOfRunningPodMigrationJob map[int]int
		expectIdx                   []int
	}{
		{10, 5, 2, map[int]int{4: 0, 8: 0}, map[int]int{3: 0, 1: 1}, []int{4, 8, 0, 1, 2, 3, 4, 6, 7, 9}},
		{10, 2, 2, map[int]int{}, map[int]int{}, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{5, 2, 2, map[int]int{0: 0, 1: 0, 2: 0, 3: 0, 4: 0}, map[int]int{0: 0}, []int{0, 1, 2, 3, 4}},
	}

	for _, testCase := range testCases {
		fakeClient := fake.NewClientBuilder().Build()

		creationTime := time.Now()
		podMigrationJobs := make([]*v1alpha1.PodMigrationJob, testCase.podMigrationJobNums)
		runningPodMigrationJobs := make([]*v1alpha1.PodMigrationJob, testCase.runningPodMigrationJobNums)
		jobs := make([]*batchv1.Job, testCase.jobNums)

		// create jobs
		for k := 0; k < testCase.jobNums; k++ {
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
			err := fakeClient.Create(context.TODO(), job)
			if err != nil {
				t.Errorf("fail to create job %v", err)
				return
			}
		}
		// create PodMigrationJobs
		for k := 0; k < testCase.podMigrationJobNums; k++ {
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
			err := fakeClient.Create(context.TODO(), pod)
			if err != nil {
				t.Errorf("fail to create pod %v", err)
				return
			}
			podMigrationJobs = append(podMigrationJobs, job)
		}

		// create Running PodMigrationJobs
		for k := 0; k < testCase.podMigrationJobNums; k++ {
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
			err := fakeClient.Create(context.TODO(), pod)
			if err != nil {
				t.Errorf("fail to create pod %v", pod)
				return
			}
			runningPodMigrationJobs = append(runningPodMigrationJobs, job)
		}

		sort := NewJobGatherSortFn(fakeClient)
		var actualPodMigrationJobs []*v1alpha1.PodMigrationJob
		copy(actualPodMigrationJobs, podMigrationJobs)
		sort(actualPodMigrationJobs)

		for i, job := range actualPodMigrationJobs {
			assert.Equal(t, podMigrationJobs[testCase.expectIdx[i]], job)
		}
	}
}

func TestJobGatherSortFn(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewClientBuilder().Build()
	pods := make([]*corev1.Pod, 10)
	podMigrationJobs := make([]*v1alpha1.PodMigrationJob, 10)

	// create jobs
	jobs := []*batchv1.Job{
		{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Job",
				APIVersion: "batch/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job1",
				Namespace: "defualt",
				UID:       "4fb33a91-7703-4b1d-99db-24ff5d8bbfab",
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{},
			},
		},
		{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Job",
				APIVersion: "batch/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job2",
				Namespace: "defualt",
				UID:       "3fb33a91-7703-4b1d-99db-24ff5d8bbfab",
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{},
			},
		},
	}

	// create pods
	for i := 0; i < len(pods); i++ {
		pods[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: apitypes.DefaultNamespace,
				Name:      "test-pod-" + strconv.Itoa(i),
			},
		}

		podMigrationJobs[i] = &v1alpha1.PodMigrationJob{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: apitypes.DefaultNamespace,
				Name:      "test-job-" + strconv.Itoa(i),
			},
			Spec: v1alpha1.PodMigrationJobSpec{
				PodRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Namespace: apitypes.DefaultNamespace,
					Name:      pods[i].Name,
				},
			},
		}
	}

	pods[1].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(jobs[0])})
	pods[4].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(jobs[0])})
	pods[9].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(jobs[0])})
	pods[5].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(jobs[1])})
	pods[3].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(jobs[1])})

	rankOfJobs := map[*v1alpha1.PodMigrationJob]int{
		podMigrationJobs[0]: 0,
		podMigrationJobs[1]: 1,
		podMigrationJobs[2]: 4,
		podMigrationJobs[3]: 5,
		podMigrationJobs[4]: 2,
		podMigrationJobs[5]: 6,
		podMigrationJobs[6]: 7,
		podMigrationJobs[7]: 8,
		podMigrationJobs[8]: 9,
		podMigrationJobs[9]: 3,
	}

	// create jobs
	for i := 0; i < len(jobs); i++ {
		job := jobs[i]
		if err := fakeClient.Create(ctx, job); err != nil {
			t.Errorf("failed to create job %v", err)
			return
		}
	}

	// create pod
	for i := 0; i < len(pods); i++ {
		if i == 6 {
			continue
		}
		pod := pods[i]
		if err := fakeClient.Create(ctx, pod); err != nil {
			t.Errorf("failed to create pod %v", err)
			return
		}
	}

	// create func
	f := NewJobGatherSortFn(fakeClient)
	migrationJobs := f(podMigrationJobs)

	for i, job := range migrationJobs {
		assert.Equal(t, rankOfJobs[job], i)
	}
}

func TestDisperseByWorkloadSortFn(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewClientBuilder().Build()
	pods := make([]*corev1.Pod, 15)
	podMigrationJobs := make([]*v1alpha1.PodMigrationJob, 15)

	rs := &v12.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ReplicaSet",
			APIVersion: "app/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: apitypes.DefaultNamespace,
			Name:      "app-xxxx",
			UID:       "uid-app-xxx",
		},
	}

	ss := &v12.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "app/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: apitypes.DefaultNamespace,
			Name:      "ss-xxx",
			UID:       "uid-ss-xxx",
		},
	}

	// create pods
	for i := 0; i < len(pods); i++ {
		pods[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: apitypes.DefaultNamespace,
				Name:      "test-pod-" + strconv.Itoa(i),
			},
		}

		podMigrationJobs[i] = &v1alpha1.PodMigrationJob{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: apitypes.DefaultNamespace,
				Name:      "test-job-" + strconv.Itoa(i),
			},
			Spec: v1alpha1.PodMigrationJobSpec{
				PodRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Namespace: apitypes.DefaultNamespace,
					Name:      pods[i].Name,
				},
			},
		}
	}

	pods[1].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(rs)})
	pods[3].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(rs)})
	pods[4].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(rs)})
	pods[8].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(ss)})
	pods[10].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(ss)})
	pods[12].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(ss)})
	pods[13].SetOwnerReferences([]metav1.OwnerReference{getOwnerReferenceFromObj(ss)})

	rankOfJobs := map[*v1alpha1.PodMigrationJob]int{
		podMigrationJobs[0]:  0,
		podMigrationJobs[1]:  3,
		podMigrationJobs[2]:  2,
		podMigrationJobs[3]:  8,
		podMigrationJobs[4]:  12,
		podMigrationJobs[5]:  5,
		podMigrationJobs[6]:  6,
		podMigrationJobs[7]:  7,
		podMigrationJobs[8]:  1,
		podMigrationJobs[9]:  9,
		podMigrationJobs[10]: 4,
		podMigrationJobs[11]: 11,
		podMigrationJobs[12]: 10,
		podMigrationJobs[13]: 13,
		podMigrationJobs[14]: 14,
	}

	// create pod
	for i := 0; i < len(pods); i++ {
		if i == 6 {
			continue
		}
		pod := pods[i]
		if err := fakeClient.Create(ctx, pod); err != nil {
			t.Errorf("failed to create pod %v", err)
		}
	}

	if err := fakeClient.Create(ctx, rs); err != nil {
		t.Errorf("failed to create replicaset %v", err)
	}

	if err := fakeClient.Create(ctx, ss); err != nil {
		t.Errorf("failed to create statefulset %v", err)
	}

	// create func
	f := NewDisperseByWorkloadSortFn(fakeClient)
	migrationJobs := f(podMigrationJobs)

	for i, job := range migrationJobs {
		assert.Equal(t, rankOfJobs[job], i)
	}
}

func getOwnerReferenceFromObj(obj any) metav1.OwnerReference {
	controller := true
	blockOwnerDeletion := true

	owner := metav1.OwnerReference{
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}
	switch reflect.TypeOf(obj).String() {
	case "*v1.Job":
		job := obj.(*batchv1.Job)
		owner.Name = job.Name
		owner.UID = job.UID
		owner.Kind = job.Kind
		owner.APIVersion = job.APIVersion
	case "*v1.ReplicaSet":
		rs := obj.(*v12.ReplicaSet)
		owner.Name = rs.Name
		owner.UID = rs.UID
		owner.Kind = rs.Kind
		owner.APIVersion = rs.APIVersion
	case "*v1.StatefulSet":
		ss := obj.(*v12.StatefulSet)
		owner.Name = ss.Name
		owner.UID = ss.UID
		owner.Kind = ss.Kind
		owner.APIVersion = ss.APIVersion
	}
	return owner
}

type podDecoratorFn func(pod *corev1.Pod)

func withCost(costType string, cost int32) podDecoratorFn {
	return func(pod *corev1.Pod) {
		pod.Annotations[costType] = strconv.Itoa(int(cost))
	}
}

func makePod(name string, priority int32, koordQoS extension.QoSClass, k8sQoS corev1.PodQOSClass, creationTime time.Time, decoratorFns ...podDecoratorFn) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelPodQoS: string(koordQoS),
			},
			Annotations:       map[string]string{},
			CreationTimestamp: metav1.Time{Time: creationTime},
			UID:               types.UID("default" + "/" + name),
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "cores/v1",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			Priority: &priority,
		},
		Status: corev1.PodStatus{
			QOSClass: k8sQoS,
		},
	}
	for _, decorator := range decoratorFns {
		decorator(pod)
	}
	return pod
}

func makePodMigrationJob(name string, creationTime time.Time, pod *corev1.Pod) *v1alpha1.PodMigrationJob {
	job := &v1alpha1.PodMigrationJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			CreationTimestamp: metav1.Time{Time: creationTime},
			UID:               types.UID(name + "uid"),
		},
	}
	if pod != nil {
		job.Spec.PodRef = &corev1.ObjectReference{
			Kind:            pod.Kind,
			Namespace:       pod.Namespace,
			Name:            pod.Name,
			UID:             pod.UID,
			APIVersion:      pod.APIVersion,
			ResourceVersion: pod.ResourceVersion,
		}
	}
	return job
}
