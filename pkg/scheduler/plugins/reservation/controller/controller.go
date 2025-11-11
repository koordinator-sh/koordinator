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

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/api/v1/resource"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	schedulinglister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	Name = "reservationController"

	minRetryAfterTime = 3 * time.Second
	maxRetryAfterTime = 15 * time.Second
)

var _ frameworkext.Controller = &Controller{}

type Controller struct {
	sharedInformerFactory      informers.SharedInformerFactory
	koordSharedInformerFactory koordinatorinformers.SharedInformerFactory
	nodeLister                 corelister.NodeLister
	podLister                  corelister.PodLister
	reservationLister          schedulinglister.ReservationLister
	client                     clientset.Interface
	koordClientSet             koordclientset.Interface
	queue                      workqueue.RateLimitingInterface
	numWorker                  int
	isGCDisabled               bool
	gcDuration                 time.Duration
	gcInterval                 time.Duration

	lock   sync.RWMutex
	pods   map[string]map[types.UID]*corev1.Pod    // nodeName -> podUID -> pod
	podToR map[types.UID]types.UID                 // podUID to reservationUID
	rToPod map[types.UID]map[types.UID]*corev1.Pod // reservationUID -> podUID -> pod
}

func New(
	sharedInformerFactory informers.SharedInformerFactory,
	koordSharedInformerFactory koordinatorinformers.SharedInformerFactory,
	client clientset.Interface,
	koordClientSet koordclientset.Interface,
	args *config.ReservationArgs,
) *Controller {
	nodeLister := sharedInformerFactory.Core().V1().Nodes().Lister()
	podLister := sharedInformerFactory.Core().V1().Pods().Lister()
	reservationLister := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Lister()

	rateLimiter := workqueue.DefaultControllerRateLimiter()
	queue := workqueue.NewNamedRateLimitingQueue(rateLimiter, Name)

	numWorker := 1
	if args != nil && args.ControllerWorkers > 0 {
		numWorker = int(args.ControllerWorkers)
	}

	isGCDisabled := false
	if args != nil && args.DisableGarbageCollection {
		isGCDisabled = true
	}
	gcDuration := defaultGCDuration
	if args != nil && args.GCDurationSeconds > 0 {
		gcDuration = time.Duration(args.GCDurationSeconds) * time.Second
	}
	gcInterval := defaultGCCheckInterval
	if args != nil && args.GCIntervalSeconds > 0 {
		gcInterval = time.Duration(args.GCIntervalSeconds) * time.Second
	}
	return &Controller{
		sharedInformerFactory:      sharedInformerFactory,
		koordSharedInformerFactory: koordSharedInformerFactory,
		nodeLister:                 nodeLister,
		podLister:                  podLister,
		reservationLister:          reservationLister,
		client:                     client,
		koordClientSet:             koordClientSet,
		queue:                      queue,
		numWorker:                  numWorker,
		isGCDisabled:               isGCDisabled,
		gcDuration:                 gcDuration,
		gcInterval:                 gcInterval,
		pods:                       map[string]map[types.UID]*corev1.Pod{},
		podToR:                     map[types.UID]types.UID{},
		rToPod:                     map[types.UID]map[types.UID]*corev1.Pod{},
	}
}

func (c *Controller) Name() string { return Name }

func (c *Controller) Start() {
	nodeInformer := c.sharedInformerFactory.Core().V1().Nodes().Informer()
	nodeInformer.AddEventHandler(&cache.ResourceEventHandlerFuncs{
		DeleteFunc: c.onNodeDelete,
	})

	podInformer := c.sharedInformerFactory.Core().V1().Pods().Informer()
	frameworkexthelper.ForceSyncFromInformer(context.Background().Done(), c.sharedInformerFactory, podInformer, &cache.ResourceEventHandlerFuncs{
		AddFunc:    c.onPodAdd,
		UpdateFunc: c.onPodUpdate,
		DeleteFunc: c.onPodDelete,
	})

	reservationInformer := c.koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Informer()
	reservationInformer.AddEventHandler(&cache.ResourceEventHandlerFuncs{
		AddFunc:    c.onReservationAdd,
		UpdateFunc: c.onReservationUpdate,
		DeleteFunc: c.onReservationDelete,
	})

	done := context.Background().Done()
	c.sharedInformerFactory.Start(done)
	c.koordSharedInformerFactory.Start(done)
	c.sharedInformerFactory.WaitForCacheSync(done)
	c.koordSharedInformerFactory.WaitForCacheSync(done)

	for i := 0; i < c.numWorker; i++ {
		go c.worker()
	}
	if !c.isGCDisabled {
		go wait.Until(c.gcReservations, c.gcInterval, nil)
	} else {
		klog.V(4).InfoS("garbage collection for reservations is disabled")
	}
}

func (c *Controller) worker() {
	for c.processNextWorkItem() {

	}
}

func (c *Controller) processNextWorkItem() bool {
	req, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(req)

	result, err := c.sync(req.(string))

	switch {
	case err != nil:
		c.queue.AddRateLimited(req)
		klog.ErrorS(err, "failed to sync Reservation")
	case result.requeueAfter > 0:
		c.queue.Forget(req)
		c.queue.AddAfter(req, result.requeueAfter)
	case result.requeue:
		c.queue.AddRateLimited(req)
	default:
		c.queue.Forget(req)
	}
	return true
}

type result struct {
	requeue      bool
	requeueAfter time.Duration
}

func (c *Controller) sync(key string) (result, error) {
	reservationName, reservationUID, err := parseReservationKey(key)
	if err != nil {
		klog.V(4).ErrorS(err, "failed to parse Reservation key", "key", key)
		return result{}, err
	}
	reservation, err := c.reservationLister.Get(reservationName)
	if errors.IsNotFound(err) {
		if k8sfeature.DefaultFeatureGate.Enabled(features.CleanExpiredReservationAllocated) {
			// Clean the reservation-allocated annotation for owner pods when a reservation is deleted.
			err = c.syncPodsForTerminatedReservation(reservationName, reservationUID)
			if err != nil {
				klog.ErrorS(err, "failed to sync for deleted Reservation", "reservation", reservationName)
				return result{}, err
			}
			klog.V(5).InfoS("sync pods for deleted Reservation finished", "reservation", reservationName, "uid", reservationUID)
		}
		return result{}, nil
	}
	if err != nil {
		klog.V(4).ErrorS(err, "failed to get Reservation", "reservation", reservationName, "uid", reservationUID)
		return result{}, nil
	}

	if reservationutil.IsReservationFailed(reservation) ||
		reservationutil.IsReservationSucceeded(reservation) {
		klog.V(6).InfoS("skipped sync failed or succeeded Reservation", "reservation", reservationName, "uid", reservationUID)
		return result{}, nil
	}

	reservation = reservation.DeepCopy()
	if err := c.syncAssignedReservation(reservation); err != nil {
		klog.V(4).ErrorS(err, "failed to sync assigned Reservation", "reservation", reservationName, "uid", reservationUID)
		return result{}, err
	}

	klog.V(5).InfoS("sync Reservation finished", "reservation", reservationName, "uid", reservationUID)
	return result{requeueAfter: nextSyncTime(reservation)}, nil
}

func (c *Controller) syncPodsForTerminatedReservation(rName string, rUID types.UID) error {
	// If the reservation is deleted, remove the reservationAllocation of the owner pods.
	pods := c.getPodsOnReservation(rUID)
	if len(pods) <= 0 {
		return nil
	}
	var errs []error
	for _, pod := range pods {
		curPod, err := c.podLister.Pods(pod.Namespace).Get(pod.Name)
		if err != nil && errors.IsNotFound(err) {
			klog.V(4).InfoS("ignored to remove reservation allocated for pod not found", "reservation", rName, "uid", rUID, "pod", klog.KObj(pod))
			continue
		}
		if err != nil {
			klog.ErrorS(err, "failed to get reservation allocated pod", "reservation", rName, "uid", rUID, "pod", klog.KObj(curPod))
			errs = append(errs, err)
			continue
		}
		modifiedPod := curPod.DeepCopy()
		removed, err := apiext.RemoveReservationAllocated(modifiedPod, &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: rName,
				UID:  rUID,
			},
		})
		if err != nil {
			klog.V(4).ErrorS(err, "failed to remove reservation allocated for the pod",
				"reservation", rName, "uid", rUID, "pod", klog.KObj(curPod))
			errs = append(errs, err)
			continue
		}
		if !removed {
			continue
		}
		_, err = util.PatchPodSafe(context.TODO(), c.client, curPod, modifiedPod)
		if err != nil {
			klog.ErrorS(err, "failed to patch reservation allocated for pod",
				"reservation", rName, "uid", rUID, "pod", klog.KObj(curPod))
			errs = append(errs, err)
			continue
		}
		klog.V(4).InfoS("successfully patch pod to remove reservation allocated",
			"reservation", rName, "uid", rUID, "pod", klog.KObj(curPod))
	}
	return utilerrors.NewAggregate(errs)
}

func (c *Controller) expireReservation(reservation *schedulingv1alpha1.Reservation) error {
	reservationutil.SetReservationExpired(reservation)
	return c.updateReservationStatus(reservation)
}

func (c *Controller) syncAssignedReservation(reservation *schedulingv1alpha1.Reservation) error {
	if reservation.Status.NodeName == "" {
		return nil
	}
	// use a pods snapshot to avoid the inconsistency between pods and reservation status
	pods := c.getPodsOnNode(reservation.Status.NodeName)
	if err := c.syncStatus(reservation, pods); err != nil {
		return fmt.Errorf("sync reservation status failed, err: %w", err)
	}
	return nil
}

func (c *Controller) syncStatus(reservation *schedulingv1alpha1.Reservation, pods map[types.UID]*corev1.Pod) error {
	if isReservationNeedExpiration(reservation) {
		return c.expireReservation(reservation)
	}

	if reservation.Status.NodeName != "" && missingNode(reservation, c.nodeLister) {
		return c.expireReservation(reservation)
	}

	if reservation.Status.NodeName == "" {
		RecordReservationPhases(reservation)
		return nil
	}

	var actualOwners []corev1.ObjectReference
	var actualAllocated corev1.ResourceList
	for _, pod := range pods {
		reservationAllocated, err := apiext.GetReservationAllocated(pod)
		if err != nil || reservationAllocated == nil || reservationAllocated.UID != reservation.UID {
			continue
		}

		actualOwners = append(actualOwners, corev1.ObjectReference{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			UID:       pod.UID,
		})
		requests := resource.PodRequests(pod, resource.PodResourcesOptions{})
		actualAllocated = quotav1.Add(actualAllocated, requests)
	}

	sort.Slice(reservation.Status.CurrentOwners, func(i, j int) bool {
		return reservation.Status.CurrentOwners[i].UID < reservation.Status.CurrentOwners[j].UID
	})
	sort.Slice(actualOwners, func(i, j int) bool {
		return actualOwners[i].UID < actualOwners[j].UID
	})

	actualAllocated = quotav1.Mask(actualAllocated, quotav1.ResourceNames(reservation.Status.Allocatable))
	if reflect.DeepEqual(reservation.Status.CurrentOwners, actualOwners) && quotav1.Equals(actualAllocated, reservation.Status.Allocated) {
		return nil
	}

	reservation.Status.Allocated = actualAllocated
	reservation.Status.CurrentOwners = actualOwners

	if apiext.IsReservationAllocateOnce(reservation) {
		reservationutil.SetReservationSucceeded(reservation)
	}
	RecordReservationResource(reservation) // must be called after actualAllocated

	return c.updateReservationStatus(reservation)
}

func (c *Controller) updateReservationStatus(reservation *schedulingv1alpha1.Reservation) error {
	RecordReservationPhases(reservation)
	_, err := c.koordClientSet.SchedulingV1alpha1().Reservations().UpdateStatus(context.TODO(), reservation, metav1.UpdateOptions{})
	if err != nil {
		klog.ErrorS(err, "Failed to update reservation status", "reservation", klog.KObj(reservation), "uid", reservation.UID, "phase", reservation.Status.Phase)
		return err
	}
	klog.V(4).InfoS("Successfully sync reservation status", "reservation", klog.KObj(reservation), "uid", reservation.UID, "phase", reservation.Status.Phase)
	return nil
}

func isReservationNeedExpiration(r *schedulingv1alpha1.Reservation) bool {
	// 1. failed or succeeded reservations does not need to expire
	if reservationutil.IsReservationFailed(r) || reservationutil.IsReservationSucceeded(r) {
		return false
	}
	// 2. disable expiration if TTL is set as 0
	if r.Spec.TTL != nil && r.Spec.TTL.Duration == 0 {
		return false
	}
	// 3. if both TTL and Expires are set, firstly check Expires
	return r.Spec.Expires != nil && time.Now().After(r.Spec.Expires.Time) ||
		r.Spec.TTL != nil && time.Since(r.CreationTimestamp.Time) > r.Spec.TTL.Duration
}

func nextSyncTime(r *schedulingv1alpha1.Reservation) time.Duration {
	if reservationutil.IsReservationFailed(r) || reservationutil.IsReservationSucceeded(r) {
		return 0
	}
	var duration time.Duration
	if r.Spec.Expires != nil {
		duration = time.Until(r.Spec.Expires.Time)
	} else if r.Spec.TTL != nil && r.Spec.TTL.Duration > 0 {
		duration = time.Until(r.CreationTimestamp.Add(r.Spec.TTL.Duration))
	}
	if duration == 0 {
		return 0
	}
	if duration < minRetryAfterTime {
		duration = minRetryAfterTime
	} else if duration > maxRetryAfterTime {
		duration = maxRetryAfterTime
	}
	return duration
}

// RecordReservationPhases records all possible phases of a reservation as metrics.
// For each phase, it sets the value to 1.0 if it matches the current phase of the reservation,
// otherwise, it sets the value to 0.0.
func RecordReservationPhases(reservation *schedulingv1alpha1.Reservation) {
	allPhases := []struct {
		name string
	}{
		{string(schedulingv1alpha1.ReservationPending)},
		{string(schedulingv1alpha1.ReservationAvailable)},
		{string(schedulingv1alpha1.ReservationSucceeded)},
		{string(schedulingv1alpha1.ReservationFailed)},
	}

	boolFloat64 := func(b bool) float64 {
		if b {
			return 1.0
		}
		return 0.0
	}

	currentPhase := reservation.Status.Phase
	for _, phase := range allPhases {
		isCurrentPhase := false
		if currentPhase == "" {
			// If the reservation doesn't have a phase yet,
			// consider the pending phase to be the current phase.
			isCurrentPhase = phase.name == string(schedulingv1alpha1.ReservationPending)
		} else {
			isCurrentPhase = currentPhase == schedulingv1alpha1.ReservationPhase(phase.name)
		}
		// Record the phase with a value of 1.0 if it's the current phase, otherwise 0.0.
		metrics.RecordReservationPhase(reservation.Name, phase.name, boolFloat64(isCurrentPhase))
	}
}

func RecordReservationResource(reservation *schedulingv1alpha1.Reservation) {
	if reservation.Status.Allocatable == nil {
		return
	}
	const (
		MilliCorePerCore = 1000
		BytesPerGiB      = 1024 * 1024 * 1024
	)

	resources := quotav1.ResourceNames(reservation.Status.Allocatable)
	for _, resourceName := range resources {
		allocatable := reservation.Status.Allocatable[resourceName]
		allocated := reservation.Status.Allocated[resourceName]

		var allocatableVal, allocatedVal float64
		var unit string

		switch resourceName {
		case corev1.ResourceCPU:
			// mCPU -> Core
			allocatableVal = float64(allocatable.MilliValue()) / MilliCorePerCore
			allocatedVal = float64(allocated.MilliValue()) / MilliCorePerCore
			unit = metrics.UnitCore
		case corev1.ResourceMemory:
			// bytes -> GiB
			allocatableVal = float64(allocatable.Value()) / BytesPerGiB
			allocatedVal = float64(allocated.Value()) / BytesPerGiB
			unit = metrics.UnitGiB
		default:
			allocatableVal = float64(allocatable.Value())
			allocatedVal = float64(allocated.Value())
			unit = metrics.UnitRaw
		}

		// allocatable
		metrics.RecordReservationResourceByTypeWithUnit(
			reservation.Name, string(resourceName), metrics.TypeAllocatable, unit, allocatableVal)

		// allocated
		metrics.RecordReservationResourceByTypeWithUnit(
			reservation.Name, string(resourceName), metrics.TypeAllocated, unit, allocatedVal)

		// utilization
		if allocatableVal > 0 {
			utilization := allocatedVal / allocatableVal
			metrics.RecordReservationResourceByTypeWithUnit(
				reservation.Name, string(resourceName), metrics.TypeUtilization, metrics.UnitRatio, utilization)
		}
	}
}
