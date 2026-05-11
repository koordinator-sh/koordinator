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

package migration

import (
	"context"
	"errors"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sev1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/evictor"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
)

type errorClient struct {
	client.Client
	createErr error
}

func (e *errorClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return e.createErr
}

func TestReconcilerEvictDryRun(t *testing.T) {
	reconciler := newTestReconciler()
	reconciler.args.DryRun = true

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
	}

	ok := reconciler.Evict(context.Background(), pod, framework.EvictOptions{
		PluginName: "test-plugin",
		Reason:     "dry-run",
	})
	assert.True(t, ok)

	jobList := &sev1alpha1.PodMigrationJobList{}
	err := reconciler.Client.List(context.Background(), jobList)
	assert.NoError(t, err)
	assert.Len(t, jobList.Items, 0)
}

func TestReconcilerEvictFilterFailed(t *testing.T) {
	reconciler := newTestReconciler()
	reconciler.arbitrator = &fakeArbitrator{
		filter: func(*corev1.Pod) bool {
			return false
		},
		preEvictionFilter: func(*corev1.Pod) bool {
			return true
		},
		add:    func(*sev1alpha1.PodMigrationJob) {},
		delete: func(types.UID) {},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
	}

	ok := reconciler.Evict(context.Background(), pod, framework.EvictOptions{
		PluginName: "test-plugin",
		Reason:     "filtered",
	})
	assert.False(t, ok)

	jobList := &sev1alpha1.PodMigrationJobList{}
	err := reconciler.Client.List(context.Background(), jobList)
	assert.NoError(t, err)
	assert.Len(t, jobList.Items, 0)
}

func TestReconcilerEvictObjectLimiterExceeded(t *testing.T) {
	reconciler := newTestReconciler()
	reconciler.args.ObjectLimiters = deschedulerconfig.ObjectLimiterMap{
		deschedulerconfig.MigrationLimitObjectNamespace: {
			Duration: metav1.Duration{Duration: time.Minute},
		},
	}
	reconciler.limiterMap = map[deschedulerconfig.MigrationLimitObjectType]map[string]*rate.Limiter{
		deschedulerconfig.MigrationLimitObjectNamespace: {
			"default": rate.NewLimiter(0, 0),
		},
	}
	reconciler.limiterCacheMap = map[deschedulerconfig.MigrationLimitObjectType]*gocache.Cache{
		deschedulerconfig.MigrationLimitObjectNamespace: gocache.New(time.Minute, time.Minute),
	}
	reconciler.arbitrator = &fakeArbitrator{
		filter: func(*corev1.Pod) bool {
			return true
		},
		preEvictionFilter: func(*corev1.Pod) bool {
			return true
		},
		add:    func(*sev1alpha1.PodMigrationJob) {},
		delete: func(types.UID) {},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
	}

	ok := reconciler.Evict(context.Background(), pod, framework.EvictOptions{
		PluginName: "test-plugin",
		Reason:     "limited",
	})
	assert.False(t, ok)

	jobList := &sev1alpha1.PodMigrationJobList{}
	err := reconciler.Client.List(context.Background(), jobList)
	assert.NoError(t, err)
	assert.Len(t, jobList.Items, 0)
}

func TestReconcilerEvictCreateError(t *testing.T) {
	reconciler := newTestReconciler()
	reconciler.args.DefaultDeleteOptions = &metav1.DeleteOptions{}
	reconciler.Client = &errorClient{
		Client:    reconciler.Client,
		createErr: errors.New("create error"),
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			UID:       types.UID("pod-uid"),
		},
	}

	ok := reconciler.Evict(context.Background(), pod, framework.EvictOptions{
		PluginName: "test-plugin",
		Reason:     "create-error",
	})
	assert.False(t, ok)
}

func TestReconcilerEvictSuccess(t *testing.T) {
	reconciler := newTestReconciler()
	reconciler.args.DefaultDeleteOptions = &metav1.DeleteOptions{}
	reconciler.reconcilerUID = types.UID("reconciler-uid")

	origUUIDGenerateFn := UUIDGenerateFn
	UUIDGenerateFn = func() types.UID {
		return types.UID("job-uid")
	}
	t.Cleanup(func() {
		UUIDGenerateFn = origUUIDGenerateFn
	})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			UID:       types.UID("pod-uid"),
		},
	}

	ok := reconciler.Evict(context.Background(), pod, framework.EvictOptions{
		PluginName: "test-plugin",
		Reason:     "success",
	})
	assert.True(t, ok)

	job := &sev1alpha1.PodMigrationJob{}
	err := reconciler.Client.Get(context.Background(), types.NamespacedName{Name: "job-uid"}, job)
	assert.NoError(t, err)
	assert.Equal(t, pod.Namespace, job.Spec.PodRef.Namespace)
	assert.Equal(t, pod.Name, job.Spec.PodRef.Name)
	assert.Equal(t, pod.UID, job.Spec.PodRef.UID)
	assert.Equal(t, "reconciler-uid", job.Annotations[AnnotationJobCreatedBy])
}

func TestCreatePodMigrationJob(t *testing.T) {
	reconciler := newTestReconciler()
	gracePeriodSeconds := int64(30)
	args := &deschedulerconfig.MigrationControllerArgs{
		DefaultJobMode: string(sev1alpha1.PodMigrationJobModeReservationFirst),
		DefaultJobTTL:  metav1.Duration{Duration: 5 * time.Minute},
		DefaultDeleteOptions: &metav1.DeleteOptions{
			GracePeriodSeconds: &gracePeriodSeconds,
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			UID:       types.UID("pod-uid"),
		},
	}
	evictOptions := framework.EvictOptions{
		PluginName: "test-plugin",
		Reason:     "test-reason",
	}
	timeout := 2 * time.Minute
	jobCtx := &JobContext{
		Labels: map[string]string{
			"ctx-label": "label-value",
		},
		Annotations: map[string]string{
			"ctx-annotation": "annotation-value",
		},
		Timeout: &timeout,
		Mode:    sev1alpha1.PodMigrationJobModeEvictionDirectly,
	}

	origUUIDGenerateFn := UUIDGenerateFn
	UUIDGenerateFn = func() types.UID {
		return types.UID("job-uid")
	}
	t.Cleanup(func() {
		UUIDGenerateFn = origUUIDGenerateFn
	})

	ctx := WithContext(context.Background(), jobCtx)
	err := CreatePodMigrationJob(ctx, pod, evictOptions, reconciler.Client, args, types.UID("reconciler-uid"))
	assert.NoError(t, err)

	job := &sev1alpha1.PodMigrationJob{}
	err = reconciler.Client.Get(ctx, types.NamespacedName{Name: "job-uid"}, job)
	assert.NoError(t, err)
	assert.Equal(t, "test-reason", job.Annotations[evictor.AnnotationEvictReason])
	assert.Equal(t, "test-plugin", job.Annotations[evictor.AnnotationEvictTrigger])
	assert.Equal(t, "reconciler-uid", job.Annotations[AnnotationJobCreatedBy])
	assert.Equal(t, "annotation-value", job.Annotations["ctx-annotation"])
	assert.Equal(t, "label-value", job.Labels["ctx-label"])
	assert.Equal(t, sev1alpha1.PodMigrationJobPending, job.Status.Phase)
	assert.Equal(t, pod.Namespace, job.Spec.PodRef.Namespace)
	assert.Equal(t, pod.Name, job.Spec.PodRef.Name)
	assert.Equal(t, pod.UID, job.Spec.PodRef.UID)
	assert.Equal(t, jobCtx.Mode, job.Spec.Mode)
	assert.NotNil(t, job.Spec.TTL)
	assert.Equal(t, timeout, job.Spec.TTL.Duration)
	assert.Equal(t, args.DefaultDeleteOptions, job.Spec.DeleteOptions)
}

func TestCreatePodMigrationJobApplyContextError(t *testing.T) {
	reconciler := newTestReconciler()
	args := &deschedulerconfig.MigrationControllerArgs{
		DefaultJobMode: string(sev1alpha1.PodMigrationJobModeReservationFirst),
		DefaultJobTTL:  metav1.Duration{Duration: time.Minute},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			UID:       types.UID("pod-uid"),
		},
	}
	evictOptions := framework.EvictOptions{
		PluginName: "test-plugin",
		Reason:     "test-reason",
	}
	applyErr := errors.New("apply error")
	origApplyJobContextFn := applyJobContextFn
	applyJobContextFn = func(context.Context, *sev1alpha1.PodMigrationJob) error {
		return applyErr
	}
	t.Cleanup(func() {
		applyJobContextFn = origApplyJobContextFn
	})

	err := CreatePodMigrationJob(context.Background(), pod, evictOptions, reconciler.Client, args, types.UID("reconciler-uid"))
	assert.ErrorIs(t, err, applyErr)

	jobList := &sev1alpha1.PodMigrationJobList{}
	listErr := reconciler.Client.List(context.Background(), jobList)
	assert.NoError(t, listErr)
	assert.Len(t, jobList.Items, 0)
}
