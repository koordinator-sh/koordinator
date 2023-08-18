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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	for _, pod := range pods {
		err := fakeClient.Create(context.TODO(), pod)
		if err != nil {
			t.Errorf("failed to create pod, err: %v", err)
		}
	}
	collection := map[types.UID]*v1alpha1.PodMigrationJob{}
	for _, job := range collection {
		collection[job.UID] = job
	}
	arbitrator := &DefaultArbitrator{
		collection: collection,
		client:     fakeClient,
		sorts: []SortFn{
			NewPodSortFn(fakeClient, sorter.PodSorter()),
			NewDisperseByWorkloadSortFn(fakeClient),
			NewJobGatherSortFn(fakeClient),
			NewJobMigratingSortFn(fakeClient),
		},
	}
	arbitrator.Sort(jobs)
	expectedPodsOrder := []string{"test-1", "test-12", "test-18", "test-16", "test-19", "test-17", "test-15", "test-21", "test-20", "test-23", "test-22", "test-9", "test-8", "test-11", "test-10", "test-13", "test-14", "test-2", "test-3", "test-7", "test-4", "test-6", "test-5"}
	var podsOrder []string
	for _, v := range jobs {
		podsOrder = append(podsOrder, v.Name)
	}
	assert.Equal(t, expectedPodsOrder, podsOrder)
}

func TestGroupFilter(t *testing.T) {
	// TODO
}

func TestSelect(t *testing.T) {
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
	for _, job := range migratingJobs {
		err := fakeClient.Create(context.TODO(), job)
		if err != nil {
			t.Errorf("fail to create job, err: %v", err)
		}
	}
	arbitrator := &DefaultArbitrator{
		collection: map[types.UID]*v1alpha1.PodMigrationJob{
			migratingJobs[0].UID: migratingJobs[0],
			migratingJobs[1].UID: migratingJobs[1],
			migratingJobs[2].UID: migratingJobs[2],
			migratingJobs[3].UID: migratingJobs[3],
			migratingJobs[4].UID: migratingJobs[4],
		},
		queue:  workqueue.NewRateLimitingQueue(&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(1, 1)}),
		client: fakeClient,
	}
	expectedJobs := []string{"test-job-1"}
	arbitrator.Select(migratingJobs[1:])

	var actualJobs []string
	for _, job := range arbitrator.collection {
		actualJobs = append(actualJobs, job.Name)
	}
	assert.ElementsMatchf(t, expectedJobs, actualJobs, "failed")
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
		collection: map[types.UID]*v1alpha1.PodMigrationJob{},
		queue:      workqueue.NewRateLimitingQueue(&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(1, 1)}),
		client:     fakeClient,
	}

	for _, job := range migratingJobs {
		arbitrator.AddJob(job)
	}

	var actualJobs []string
	for _, job := range arbitrator.collection {
		actualJobs = append(actualJobs, job.Name)
	}
	expectedJobs := []string{"test-job-1", "test-job-2", "test-job-3", "test-job-4", "test-job-5"}
	assert.ElementsMatchf(t, expectedJobs, actualJobs, "failed")
}

func TestDoOnceArbitrate(t *testing.T) {
	// TODO
}

func TestArbitrate(t *testing.T) {
	// TODO
}
