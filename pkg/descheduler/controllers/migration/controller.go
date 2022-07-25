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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	sev1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/evictor"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/reservation"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/util"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/names"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/options"
	evictionsutil "github.com/koordinator-sh/koordinator/pkg/descheduler/evictions"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

const (
	Name                = names.MigrationController
	defaultRequeueAfter = 3 * time.Second
)

var _ framework.Evictor = &Reconciler{}

type Reconciler struct {
	client.Client
	args                   *deschedulerconfig.MigrationControllerArgs
	eventRecorder          events.EventRecorder
	reservationInterpreter reservation.Interpreter
	evictorInterpreter     evictor.Interpreter
	evictorFilter          *evictionsutil.EvictorFilter
	assumedCache           *assumedCache
	clock                  clock.Clock
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	controllerArgs, ok := args.(*deschedulerconfig.MigrationControllerArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type MigrationControllerArgs, got %T", args)
	}

	r, err := newReconciler(controllerArgs, handle)
	if err != nil {
		return nil, err
	}

	// TODO(joseph): custom MaxConcurrentReconciles via configuration
	c, err := controller.New(Name, options.Manager, controller.Options{Reconciler: r, MaxConcurrentReconciles: 1})
	if err != nil {
		return nil, err
	}

	if err = c.Watch(&source.Kind{Type: &sev1alpha1.PodMigrationJob{}}, &handler.EnqueueRequestForObject{}, &predicate.Funcs{
		DeleteFunc: func(event event.DeleteEvent) bool {
			job := event.Object.(*sev1alpha1.PodMigrationJob)
			r.assumedCache.delete(job)
			return true
		}}); err != nil {
		return nil, err
	}
	if err = c.Watch(&source.Kind{Type: r.reservationInterpreter.GetReservationType()}, &handler.Funcs{}); err != nil {
		return nil, err
	}
	return r, nil
}

func newReconciler(controllerArgs *deschedulerconfig.MigrationControllerArgs, handle framework.Handle) (*Reconciler, error) {
	manager := options.Manager
	reservationInterpreter := reservation.NewInterpreter(manager)
	evictorInterpreter, err := evictor.NewInterpreter(controllerArgs.EvictionPolicy, handle.ClientSet())
	if err != nil {
		return nil, err
	}

	nodesGetter := func() ([]*corev1.Node, error) {
		nodesLister := handle.SharedInformerFactory().Core().V1().Nodes().Lister()
		return nodesLister.List(labels.Everything())
	}

	var selector labels.Selector
	if controllerArgs.LabelSelector != nil {
		selector, err = metav1.LabelSelectorAsSelector(controllerArgs.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to get label selectors: %v", err)
		}
	}

	evictorFilter := evictionsutil.NewEvictorFilter(
		nodesGetter,
		handle.GetPodsAssignedToNodeFunc(),
		controllerArgs.EvictLocalStoragePods,
		controllerArgs.EvictSystemCriticalPods,
		controllerArgs.IgnorePvcPods,
		controllerArgs.EvictFailedBarePods,
		evictionsutil.WithLabelSelector(selector),
	)

	r := &Reconciler{
		Client:                 manager.GetClient(),
		args:                   controllerArgs,
		eventRecorder:          handle.EventRecorder(),
		reservationInterpreter: reservationInterpreter,
		evictorInterpreter:     evictorInterpreter,
		evictorFilter:          evictorFilter,
		assumedCache:           newAssumedCache(),
		clock:                  clock.RealClock{},
	}
	err = manager.Add(r)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Reconciler) Name() string {
	return Name
}

// Filter checks if a pod can be evicted
func (r *Reconciler) Filter(pod *corev1.Pod) bool {
	return r.evictorFilter.Filter(pod)
}

// Evict evicts a pod (no pre-check performed)
func (r *Reconciler) Evict(ctx context.Context, pod *corev1.Pod, evictOptions framework.EvictOptions) bool {
	if evictOptions.DeleteOptions == nil {
		evictOptions.DeleteOptions = r.args.DefaultDeleteOptions
	}
	job := &sev1alpha1.PodMigrationJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: string(uuid.NewUUID()),
		},
		Spec: sev1alpha1.PodMigrationJobSpec{
			PodRef: &corev1.ObjectReference{
				Namespace: pod.Namespace,
				Name:      pod.Name,
				UID:       pod.UID,
			},
			// TODO(joseph): custom migration mode via configuration
			Mode: sev1alpha1.PodMigrationJobModeReservationFirst,
			// TODO(joseph): custom default TTL via configuration
			TTL:           &metav1.Duration{Duration: 5 * time.Minute},
			DeleteOptions: evictOptions.DeleteOptions,
		},
		Status: sev1alpha1.PodMigrationJobStatus{
			Phase: sev1alpha1.PodMigrationJobPending,
		},
	}
	err := r.Client.Create(ctx, job)
	if err != nil {
		klog.Errorf("Failed to create PodMigrationJob for Pod %s/s, err: %v", pod.Namespace, pod.Name, err)
		return false
	}
	return true
}

func (r *Reconciler) Start(ctx context.Context) error {
	r.scavenger(ctx.Done())
	return nil
}

func (r *Reconciler) scavenger(stopCh <-chan struct{}) {
	for {
		r.doScavenge()
		select {
		case <-stopCh:
			return
		case <-time.After(1 * time.Minute):
		}
	}
}

func (r *Reconciler) doScavenge() {
	jobList := &sev1alpha1.PodMigrationJobList{}
	opts := &client.ListOptions{
		LabelSelector: labels.Everything(),
	}
	err := r.Client.List(context.TODO(), jobList, opts, utilclient.DisableDeepCopy)
	if err != nil {
		return
	}
	for i := range jobList.Items {
		v := &jobList.Items[i]
		timeoutDuration := 30 * time.Minute
		if v.Spec.TTL != nil && v.Spec.TTL.Duration > 0 {
			timeoutDuration = v.Spec.TTL.Duration + 5*time.Minute
		}
		if r.clock.Since(v.CreationTimestamp.Time) < timeoutDuration {
			continue
		}
		if err := r.deleteReservation(context.TODO(), v); err != nil {
			if !errors.IsNotFound(err) {
				break
			}
		}
		err = r.Client.Delete(context.TODO(), v)
		if err != nil {
			klog.Errorf("Failed to scavenge PodMigrationJob %s, err: %v", v.Name, err)
		} else {
			klog.V(4).Infof("Successfully scavenge PodMigrationJob %s", v.Name)
		}
	}
}

// +kubebuilder:rbac:groups=scheduling.koordinator.sh,resources=podmigrationjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=scheduling.koordinator.sh,resources=podmigrationjobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=scheduling.koordinator.sh,resources=reservations,verbs=get;list;watch;create;update;patch;delete

// Reconcile reads that state of the cluster for a PodMigrationJob object and makes changes based on the state read
// and what is in the Spec
// Automatically generate RBAC rules to allow the Controller to read and write Deployments
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	job := &sev1alpha1.PodMigrationJob{}
	err := r.Client.Get(ctx, request.NamespacedName, job)
	if errors.IsNotFound(err) {
		return reconcile.Result{}, nil
	}
	if err != nil {
		klog.Errorf("Failed to Get PodMigrationJob from %v, err: %v", request, err)
		return reconcile.Result{}, err
	}

	if !r.assumedCache.isNewOrSameObj(job) {
		return reconcile.Result{}, nil
	}

	result, err := r.doMigrate(ctx, job)
	if err != nil {
		klog.Errorf("Failed to reconcile MigrationJob %v, err: %v", request.NamespacedName, err)
	}
	r.assumedCache.assume(job)
	return result, err
}

func (r *Reconciler) doMigrate(ctx context.Context, job *sev1alpha1.PodMigrationJob) (reconcile.Result, error) {
	klog.V(4).Infof("begin process MigrationJob %s", job.Name)
	if job.Spec.Paused {
		return reconcile.Result{}, nil
	}

	if job.Status.Phase != "" &&
		job.Status.Phase != sev1alpha1.PodMigrationJobPending &&
		job.Status.Phase != sev1alpha1.PodMigrationJobRunning {
		return reconcile.Result{}, nil
	}

	timeout, err := r.abortJobIfTimeout(ctx, job)
	if err != nil {
		return reconcile.Result{}, err
	} else if timeout {
		return reconcile.Result{}, nil
	}

	if job.Status.Phase == "" || job.Status.Phase == sev1alpha1.PodMigrationJobPending {
		job.Status.Phase = sev1alpha1.PodMigrationJobRunning
	}

	if job.Spec.Mode == sev1alpha1.PodMigrationJobModeEvictionDirectly {
		return r.evictPodDirectly(ctx, job)
	}

	if job.Spec.ReservationOptions == nil || job.Spec.ReservationOptions.ReservationRef == nil {
		err = r.createReservation(ctx, job)
		return reconcile.Result{}, err
	}

	if err = r.handleCreateReservationSuccess(ctx, job); err != nil {
		return reconcile.Result{}, err
	}

	reservationObj, err := r.reservationInterpreter.GetReservation(ctx, job.Spec.ReservationOptions.ReservationRef)
	if errors.IsNotFound(err) {
		err = r.abortJobByMissingReservation(ctx, job)
		return reconcile.Result{}, err
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	if err = r.handleScheduleFailed(ctx, job, reservationObj); err != nil {
		return reconcile.Result{}, err
	}

	if reservationObj.IsPending() {
		klog.V(4).Infof("MigrationJob %s is waiting for Reservation %s scheduled", job.Name, reservationObj)
		return reconcile.Result{RequeueAfter: defaultRequeueAfter}, nil
	}

	if !reservationObj.IsScheduled() {
		preemption := r.reservationInterpreter.Preemption()
		if !reservationObj.NeedPreemption() || preemption == nil {
			err := r.abortJobByUnschedulable(ctx, job, reservationObj)
			return reconcile.Result{}, err
		}
		preemptComplete, result, err := preemption.Preempt(ctx, job, reservationObj)
		if err != nil {
			return result, err
		} else if !preemptComplete {
			return result, nil
		}
	}

	if err = r.handleScheduleSuccess(ctx, job, reservationObj); err != nil {
		return reconcile.Result{}, err
	}

	if util.IsMigratePendingPod(reservationObj) {
		return r.waitForPendingPodScheduled(ctx, job)
	}

	klog.V(4).Infof("MigrationJob %s processes scheduled Pod %s/%s", job.Name, job.Spec.PodRef.Namespace, job.Spec.PodRef.Name)
	evictComplete, result, err := r.evictPod(ctx, job)
	if err != nil {
		return result, err
	} else if !evictComplete {
		return result, nil
	}

	boundComplete, result, err := r.waitForPodBindReservation(ctx, job, reservationObj)
	if err != nil {
		return result, err
	} else if !boundComplete {
		return result, nil
	}

	boundPod := reservationObj.GetBoundPod()
	podNamespacedName := types.NamespacedName{Namespace: boundPod.Namespace, Name: boundPod.Name}
	job.Status.PodRef = boundPod
	job.Status.Phase = sev1alpha1.PodMigrationJobSucceed
	job.Status.Status = "Complete"
	job.Status.Reason = ""
	job.Status.Message = fmt.Sprintf("Bind Pod %q in Reservation %q", podNamespacedName, reservationObj)

	cond := &sev1alpha1.PodMigrationJobCondition{
		Type:    sev1alpha1.PodMigrationJobConditionReservationPodBoundReservation,
		Status:  sev1alpha1.PodMigrationJobConditionStatusTrue,
		Message: job.Status.Message,
	}
	util.UpdateCondition(&job.Status, cond)
	err = r.Client.Status().Update(ctx, job)
	if err == nil {
		r.eventRecorder.Eventf(job, nil, corev1.EventTypeNormal, "Complete", "Migrating", job.Status.Message)
	}
	return reconcile.Result{}, err
}

func (r *Reconciler) abortJobIfTimeout(ctx context.Context, job *sev1alpha1.PodMigrationJob) (bool, error) {
	if job.Spec.TTL == nil || job.Spec.TTL.Duration == 0 {
		return false, nil
	}

	timeout := job.Spec.TTL.Duration
	elapsed := r.clock.Since(job.CreationTimestamp.Time)
	if elapsed < timeout {
		return false, nil
	}

	if err := r.deleteReservation(ctx, job); err != nil {
		if !errors.IsNotFound(err) {
			return true, err
		}
	}

	job.Status.Phase = sev1alpha1.PodMigrationJobFailed
	job.Status.Reason = sev1alpha1.PodMigrationJobReasonTimeout
	job.Status.Message = "Abort job caused by timeout"
	err := r.Client.Status().Update(ctx, job)
	if err == nil {
		r.eventRecorder.Eventf(job, nil, corev1.EventTypeWarning, sev1alpha1.PodMigrationJobReasonTimeout, "Migrating", job.Status.Message)
	}
	return true, err
}

func (r *Reconciler) abortJobByMissingPod(ctx context.Context, job *sev1alpha1.PodMigrationJob, podNamespacedName types.NamespacedName) error {
	job.Status.Phase = sev1alpha1.PodMigrationJobFailed
	job.Status.Reason = sev1alpha1.PodMigrationJobReasonMissingPod
	job.Status.Message = fmt.Sprintf("Abort job caused by missing Pod %q", podNamespacedName)
	err := r.Status().Update(ctx, job)
	if err == nil {
		r.eventRecorder.Eventf(job, nil, corev1.EventTypeWarning, sev1alpha1.PodMigrationJobReasonMissingPod, "Migrating", job.Status.Message)
	}
	return err
}

func (r *Reconciler) abortJobByMissingReservation(ctx context.Context, job *sev1alpha1.PodMigrationJob) error {
	reservationObjName := reservation.GetReservationNamespacedName(job.Spec.ReservationOptions.ReservationRef)
	job.Status.Phase = sev1alpha1.PodMigrationJobFailed
	job.Status.Reason = sev1alpha1.PodMigrationJobReasonMissingReservation
	job.Status.Message = fmt.Sprintf("Abort job caused by missing Reservation %q", reservationObjName)
	err := r.Client.Status().Update(ctx, job)
	if err == nil {
		r.eventRecorder.Eventf(job, nil, corev1.EventTypeWarning, sev1alpha1.PodMigrationJobReasonMissingReservation, "Migrating", job.Status.Message)
	}
	return err
}

func (r *Reconciler) abortJobByUnschedulable(ctx context.Context, job *sev1alpha1.PodMigrationJob, reservationObj reservation.Object) error {
	klog.V(4).Infof("MigrationJob %s stop migration because Reservation %q cannot be scheduled", job.Name, reservationObj)
	var message string
	unschedulableCond := reservationObj.GetUnschedulableCondition()
	if unschedulableCond != nil {
		message = unschedulableCond.Message
	}
	job.Status.Phase = sev1alpha1.PodMigrationJobFailed
	job.Status.Reason = sev1alpha1.PodMigrationJobReasonUnschedulable
	job.Status.Message = message
	return r.Client.Status().Update(ctx, job)
}

func (r *Reconciler) handleScheduleFailed(ctx context.Context, job *sev1alpha1.PodMigrationJob, reservationObj reservation.Object) error {
	_, cond := util.GetCondition(&job.Status, sev1alpha1.PodMigrationJobConditionReservationScheduled)
	if cond == nil || cond.Status == sev1alpha1.PodMigrationJobConditionStatusFalse {
		klog.V(4).Infof("MigrationJob %s checks whether Reservation %q is scheduled successfully", job.Name, reservationObj)
		unschedulableCond := reservationObj.GetUnschedulableCondition()
		if unschedulableCond != nil {
			cond = &sev1alpha1.PodMigrationJobCondition{
				Type:    sev1alpha1.PodMigrationJobConditionReservationScheduled,
				Status:  sev1alpha1.PodMigrationJobConditionStatusFalse,
				Reason:  sev1alpha1.PodMigrationJobReasonUnschedulable,
				Message: unschedulableCond.Message,
			}
			err := r.updateCondition(ctx, job, cond)
			if err == nil {
				r.eventRecorder.Eventf(job, nil, corev1.EventTypeWarning, sev1alpha1.PodMigrationJobReasonUnschedulable, "Migrating", unschedulableCond.Message)
			}
			return err
		}
	}
	return nil
}

func (r *Reconciler) waitForPodBindReservation(ctx context.Context, job *sev1alpha1.PodMigrationJob, reservationObj reservation.Object) (bool, reconcile.Result, error) {
	klog.V(4).Infof("MigrationJob %s checks whether Reservation %q binds Pod", job.Name, reservationObj)
	_, cond := util.GetCondition(&job.Status, sev1alpha1.PodMigrationJobConditionReservationPodBoundReservation)
	if cond != nil && cond.Status == sev1alpha1.PodMigrationJobConditionStatusTrue {
		return true, reconcile.Result{}, nil
	}

	boundPod := reservationObj.GetBoundPod()
	if boundPod == nil {
		cond = &sev1alpha1.PodMigrationJobCondition{
			Type:   sev1alpha1.PodMigrationJobConditionReservationPodBoundReservation,
			Status: sev1alpha1.PodMigrationJobConditionStatusFalse,
			Reason: sev1alpha1.PodMigrationJobReasonWaitForPodBindReservation,
		}
		err := r.updateCondition(ctx, job, cond)
		return false, reconcile.Result{RequeueAfter: defaultRequeueAfter}, err
	}

	return true, reconcile.Result{}, nil
}

func (r *Reconciler) evictPodDirectly(ctx context.Context, job *sev1alpha1.PodMigrationJob) (reconcile.Result, error) {
	podNamespacedName := types.NamespacedName{Namespace: job.Spec.PodRef.Namespace, Name: job.Spec.PodRef.Name}
	klog.V(4).Infof("MigrationJob %s try to evict Pod %q directly", job.Name, podNamespacedName)
	complete, result, err := r.evictPod(ctx, job)
	if err != nil {
		return result, err
	} else if !complete {
		return result, nil
	}

	job.Status.Phase = sev1alpha1.PodMigrationJobSucceed
	job.Status.Status = "Complete"
	job.Status.Reason = ""
	job.Status.Message = fmt.Sprintf("Pod %q has been evicted", podNamespacedName)
	err = r.Client.Status().Update(ctx, job)
	return reconcile.Result{}, err
}

func (r *Reconciler) evictPod(ctx context.Context, job *sev1alpha1.PodMigrationJob) (bool, reconcile.Result, error) {
	_, cond := util.GetCondition(&job.Status, sev1alpha1.PodMigrationJobConditionEviction)
	if cond != nil && cond.Status == sev1alpha1.PodMigrationJobConditionStatusTrue {
		return true, reconcile.Result{}, nil
	}

	klog.V(4).Infof("MigrationJob %s checks if the Pod needs to be evicted or waiting for the eviction to succeed", job.Name)
	pod := &corev1.Pod{}
	podNamespacedName := types.NamespacedName{Namespace: job.Spec.PodRef.Namespace, Name: job.Spec.PodRef.Name}
	err := r.Client.Get(ctx, podNamespacedName, pod)
	if errors.IsNotFound(err) {
		if job.Status.Status != string(sev1alpha1.PodMigrationJobConditionEviction) {
			err = r.abortJobByMissingPod(ctx, job, podNamespacedName)
			return false, reconcile.Result{}, err
		}

		cond = &sev1alpha1.PodMigrationJobCondition{
			Type:   sev1alpha1.PodMigrationJobConditionEviction,
			Status: sev1alpha1.PodMigrationJobConditionStatusTrue,
			Reason: sev1alpha1.PodMigrationJobReasonEvictComplete,
		}
		err = r.updateCondition(ctx, job, cond)
		if err == nil {
			r.eventRecorder.Eventf(job, nil, corev1.EventTypeNormal, sev1alpha1.PodMigrationJobReasonEvictComplete, "Migrating", "Pod %q has been evicted", podNamespacedName)
		}
		return true, reconcile.Result{}, err
	}
	if err != nil {
		klog.Errorf("Failed to get target Pod %q, MigrationJob: %s, err: %v", podNamespacedName, job.Name, err)
		return false, reconcile.Result{}, err
	}

	if cond != nil && cond.Reason == sev1alpha1.PodMigrationJobReasonEvicting {
		return false, reconcile.Result{RequeueAfter: defaultRequeueAfter}, nil
	}

	if job.Spec.DeleteOptions == nil {
		job.Spec.DeleteOptions = r.args.DefaultDeleteOptions
	}
	err = r.evictorInterpreter.Evict(ctx, job, pod)
	if err != nil {
		r.eventRecorder.Eventf(job, nil, corev1.EventTypeWarning, sev1alpha1.PodMigrationJobReasonFailedEvict, "Migrating", "Failed evict Pod %q caused by %v", podNamespacedName, err)
		return false, reconcile.Result{}, err
	}

	cond = &sev1alpha1.PodMigrationJobCondition{
		Type:    sev1alpha1.PodMigrationJobConditionEviction,
		Status:  sev1alpha1.PodMigrationJobConditionStatusFalse,
		Reason:  sev1alpha1.PodMigrationJobReasonEvicting,
		Message: fmt.Sprintf("Try to evict Pod %q", podNamespacedName),
	}
	err = r.updateCondition(ctx, job, cond)
	if err == nil {
		r.eventRecorder.Eventf(job, nil, corev1.EventTypeNormal, sev1alpha1.PodMigrationJobReasonEvicting, "Migrating", cond.Message)
	}
	return false, reconcile.Result{RequeueAfter: defaultRequeueAfter}, err
}

func (r *Reconciler) handleScheduleSuccess(ctx context.Context, job *sev1alpha1.PodMigrationJob, reservationObj reservation.Object) error {
	scheduledNodeName := reservationObj.GetScheduledNodeName()
	if scheduledNodeName == "" || job.Status.NodeName != "" {
		return nil
	}
	job.Status.NodeName = scheduledNodeName
	cond := &sev1alpha1.PodMigrationJobCondition{
		Type:   sev1alpha1.PodMigrationJobConditionReservationScheduled,
		Status: sev1alpha1.PodMigrationJobConditionStatusTrue,
	}
	err := r.updateCondition(ctx, job, cond)
	if err == nil {
		r.eventRecorder.Eventf(job, nil, corev1.EventTypeNormal, string(sev1alpha1.PodMigrationJobConditionReservationScheduled), "Migrating", "Assigned Reservation %q to node %q", reservationObj, scheduledNodeName)
	}
	return err
}

func (r *Reconciler) deleteReservation(ctx context.Context, job *sev1alpha1.PodMigrationJob) error {
	if job.Spec.ReservationOptions == nil || job.Spec.ReservationOptions.ReservationRef == nil {
		return nil
	}
	return r.reservationInterpreter.DeleteReservation(ctx, job.Spec.ReservationOptions.ReservationRef)
}

func (r *Reconciler) createReservation(ctx context.Context, job *sev1alpha1.PodMigrationJob) error {
	klog.V(4).Infof("MigrationJob %s try to create Reservation", job.Name)

	pod := &corev1.Pod{}
	podNamespacedName := types.NamespacedName{Namespace: job.Spec.PodRef.Namespace, Name: job.Spec.PodRef.Name}
	err := r.Client.Get(ctx, podNamespacedName, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			if abortErr := r.abortJobByMissingPod(ctx, job, podNamespacedName); abortErr != nil {
				klog.Errorf("Failed to abortJobByMissingPod, MigrationJob: %s, err: %v", job.Name, err)
			}
		}
		return err
	}

	reservationOptions := reservation.CreateOrUpdateReservationOptions(job, pod)
	job.Spec.ReservationOptions = reservationOptions

	reservationObj, err := r.reservationInterpreter.CreateReservation(ctx, job)
	if err != nil {
		cond := &sev1alpha1.PodMigrationJobCondition{
			Type:    sev1alpha1.PodMigrationJobConditionReservationCreated,
			Status:  sev1alpha1.PodMigrationJobConditionStatusFalse,
			Reason:  sev1alpha1.PodMigrationJobReasonFailedCreateReservation,
			Message: fmt.Sprintf("Failed to create Reservation caused by %v", err),
		}
		if r.updateCondition(ctx, job, cond) == nil {
			r.eventRecorder.Eventf(job, nil, corev1.EventTypeWarning, cond.Reason, "Migrating", job.Status.Message)
		}
		return err
	}

	job.Spec.ReservationOptions.ReservationRef = &corev1.ObjectReference{
		Kind:       reservationObj.GetObjectKind().GroupVersionKind().Kind,
		APIVersion: reservationObj.GetObjectKind().GroupVersionKind().Version,
		Namespace:  reservationObj.GetNamespace(),
		Name:       reservationObj.GetName(),
		UID:        reservationObj.GetUID(),
	}
	err = r.Client.Update(ctx, job)
	if err == nil {
		r.eventRecorder.Eventf(job, nil, corev1.EventTypeNormal, string(sev1alpha1.PodMigrationJobConditionReservationCreated), "Migrating", "Successfully create Reservation %q", reservationObj)
	}
	return err
}

func (r *Reconciler) handleCreateReservationSuccess(ctx context.Context, job *sev1alpha1.PodMigrationJob) error {
	cond := &sev1alpha1.PodMigrationJobCondition{
		Type:   sev1alpha1.PodMigrationJobConditionReservationCreated,
		Status: sev1alpha1.PodMigrationJobConditionStatusTrue,
	}
	return r.updateCondition(ctx, job, cond)
}

func (r *Reconciler) waitForPendingPodScheduled(ctx context.Context, job *sev1alpha1.PodMigrationJob) (reconcile.Result, error) {
	podNamespacedName := types.NamespacedName{Namespace: job.Spec.PodRef.Namespace, Name: job.Spec.PodRef.Name}
	klog.V(4).Infof("MigrationJob %s checks whether Pod %q is scheduled successfully", job.Name, podNamespacedName)

	pod := &corev1.Pod{}
	err := r.Client.Get(ctx, podNamespacedName, pod)
	if errors.IsNotFound(err) {
		err = r.abortJobByMissingPod(ctx, job, podNamespacedName)
		return reconcile.Result{}, err
	}
	if err != nil {
		klog.Errorf("Failed to get Pod %q, err: %v", podNamespacedName, err)
		return reconcile.Result{}, err
	}

	_, podCondition := podutil.GetPodCondition(&pod.Status, corev1.PodScheduled)
	if podCondition == nil || podCondition.Status == corev1.ConditionFalse {
		var message string
		if podCondition != nil {
			message = podCondition.Message
		}
		cond := &sev1alpha1.PodMigrationJobCondition{
			Type:    sev1alpha1.PodMigrationJobConditionPodScheduled,
			Status:  sev1alpha1.PodMigrationJobConditionStatusFalse,
			Reason:  sev1alpha1.PodMigrationJobReasonUnschedulable,
			Message: message,
		}
		err = r.updateCondition(ctx, job, cond)
		return reconcile.Result{RequeueAfter: defaultRequeueAfter}, err
	}

	job.Status.Phase = sev1alpha1.PodMigrationJobSucceed
	job.Status.Status = "Complete"
	job.Status.Reason = ""
	job.Status.Message = fmt.Sprintf("Assign Pod %q to node %q", podNamespacedName, pod.Spec.NodeName)
	updated := util.UpdateCondition(&job.Status, &sev1alpha1.PodMigrationJobCondition{
		Type:   sev1alpha1.PodMigrationJobConditionPodScheduled,
		Status: sev1alpha1.PodMigrationJobConditionStatusTrue,
	})
	if updated {
		err = r.Client.Status().Update(ctx, job)
		if err == nil {
			r.eventRecorder.Eventf(job, nil, corev1.EventTypeNormal, "Complete", "Migrating", job.Status.Message)
		}
	}
	return reconcile.Result{}, err
}

func (r *Reconciler) updateCondition(ctx context.Context, job *sev1alpha1.PodMigrationJob, cond *sev1alpha1.PodMigrationJobCondition) error {
	updated := util.UpdateCondition(&job.Status, cond)
	if updated {
		job.Status.Status = string(cond.Type)
		job.Status.Reason = cond.Reason
		job.Status.Message = cond.Message
		return r.Client.Status().Update(ctx, job)
	}
	return nil
}
