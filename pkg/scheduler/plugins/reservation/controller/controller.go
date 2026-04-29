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
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/component-helpers/resource"
	componentresource "k8s.io/component-helpers/resource"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
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

// ReservationResizeLock allows the controller to lock a reservation in the scheduler's
// in-process cache before making resize API calls. This closes the race window between
// the controller deciding to resize and the event handler processing the API change.
type ReservationResizeLock interface {
	LockReservationForResize(uid types.UID, nodeName string)
	UnlockReservationForResize(uid types.UID, nodeName string)
	// CleanStaleLocks force-unlocks any resize lock held longer than the timeout.
	// Called periodically as a safety net.
	CleanStaleLocks() int
}

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
	resyncInterval             time.Duration
	resizeLock                 ReservationResizeLock
	addToSchedulerQueueFn      func(pod *corev1.Pod)

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
	resizeLock ReservationResizeLock,
	addToSchedulerQueueFn func(pod *corev1.Pod),
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
	resyncInterval := defaultResyncInterval
	if args != nil && args.ResyncIntervalSeconds > 0 {
		resyncInterval = time.Duration(args.ResyncIntervalSeconds) * time.Second
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
		resyncInterval:             resyncInterval,
		resizeLock:                 resizeLock,
		addToSchedulerQueueFn:      addToSchedulerQueueFn,
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
	if c.resyncInterval > 0 {
		go wait.Until(c.resyncReservations, c.resyncInterval, nil)
	} else {
		klog.V(4).InfoS("resync for reservations is disabled")
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

	// Fork: resize path takes priority over normal sync.
	// Resize is triggered by a reservation update event (Generation change), not periodic resync.
	// Keeping it separate from syncAssignedReservation ensures the status-refresh logic stays clean.
	if reservation.Status.NodeName != "" && reservationutil.IsReservationSpecResourcesChanged(reservation) {
		if err := c.syncReservationResize(reservation); err != nil {
			klog.V(4).ErrorS(err, "failed to sync reservation resize", "reservation", reservationName, "uid", reservationUID)
			return result{}, err
		}
		klog.V(5).InfoS("sync Reservation resize finished", "reservation", reservationName, "uid", reservationUID)
		return result{requeueAfter: nextSyncTime(reservation)}, nil
	}

	// Fork: clear stale ResizeFailed condition if the user reverted the spec back to match allocatable.
	if reservation.Status.NodeName != "" && hasResizeFailedCondition(reservation) {
		klog.V(3).InfoS("Clearing stale ResizeFailed condition after spec reverted",
			"reservation", klog.KObj(reservation), "uid", reservation.UID)
		clearResizeFailedCondition(reservation)
		if err := c.updateReservationStatus(reservation); err != nil {
			return result{}, err
		}
		return result{requeueAfter: nextSyncTime(reservation)}, nil
	}

	// Normal path: sync allocated/ownership status only.
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

// syncReservationResize handles the resize reconciliation path.
// Triggered by a reservation update event when spec.template resources differ from status.allocatable.
// This is separate from syncAssignedReservation to keep the normal allocated-refresh logic clean.
func (c *Controller) syncReservationResize(reservation *schedulingv1alpha1.Reservation) error {
	// If ResizeFailed exists, check if the user changed the spec to a different target.
	// If the target is the same as the one that failed, skip retry to avoid loops.
	// If the target is different, the user has a new intent — clear and retry.
	if hasResizeFailedCondition(reservation) {
		newTarget := allocatableFingerprint(c.computeNewAllocatable(reservation))
		oldTarget := getResizeFailedTarget(reservation)
		if newTarget == oldTarget {
			klog.V(4).InfoS("Skipping automatic resize retry: same target as previous failure",
				"reservation", klog.KObj(reservation), "uid", reservation.UID)
			return nil
		}
		klog.V(3).InfoS("Spec changed to different target after ResizeFailed, clearing and retrying",
			"reservation", klog.KObj(reservation), "uid", reservation.UID,
			"oldTarget", oldTarget, "newTarget", newTarget)
		clearResizeFailedCondition(reservation)
	}
	klog.V(3).InfoS("Reservation spec resources changed, handling resize",
		"reservation", klog.KObj(reservation), "uid", reservation.UID, "node", reservation.Status.NodeName)
	return c.handleReservationResize(reservation)
}

// handleReservationResize handles a reservation spec resource change.
// Flow:
//  1. Shrink short path: if all resources decrease or stay the same, update status.allocatable
//     in-place (the event handler updates the cache).
//  2. Enlarge: create a fake resize pod (new UID, new resources, pinned to the original node)
//     and add it to the scheduler queue. The scheduler validates resource availability through
//     the standard scheduling cycle. The reservation stays Available throughout.
//
// Lock behavior:
//   - The controller locks the reservation in matchableOnNode before creating the fake pod.
//   - For enlarge, the lock prevents new pod allocation during resize.
//   - For shrink, the in-place allocatable update is atomic (single UpdateStatus).
func (c *Controller) handleReservationResize(reservation *schedulingv1alpha1.Reservation) error {
	// Lock in scheduler cache to prevent new pod allocation during resize.
	// This removes the reservation from matchableOnNode immediately (in-process, zero API cost).
	// The lock is released by the event handler's unlockReservationAfterResize call
	// once the resize state change is reflected in the cache.
	uid := reservation.UID
	nodeName := reservation.Status.NodeName
	c.resizeLock.LockReservationForResize(uid, nodeName)

	// If the API call fails after locking, rollback the lock so the reservation
	// remains matchable. Without this, a transient API error would permanently
	// remove the reservation from matchableOnNode.
	var committed bool
	defer func() {
		if !committed {
			klog.V(3).InfoS("Resize API call failed, rolling back cache lock",
				"reservation", klog.KObj(reservation), "uid", uid)
			c.resizeLock.UnlockReservationForResize(uid, nodeName)
		}
	}()

	newAllocatable := c.computeNewAllocatable(reservation)

	// Reject resize if any resource in the new allocatable is below the current allocated.
	// This applies to ALL resize types (shrink, enlarge, and mixed), because already-bound
	// pods cannot be "un-allocated" — the user must free pods first.
	// Example mixed case: cpu 4→8 (enlarge) + mem 8Gi→4Gi (shrink below allocated 6Gi) must be rejected.
	if isBelowAllocated(reservation.Status.Allocated, newAllocatable) {
		klog.V(3).InfoS("Reservation resize rejected: new allocatable below current allocated",
			"reservation", klog.KObj(reservation), "uid", reservation.UID,
			"allocated", reservation.Status.Allocated, "newAllocatable", newAllocatable)
		setResizeFailedCondition(reservation, "new allocatable is less than current allocated resources", newAllocatable)
		if err := c.updateReservationStatus(reservation); err != nil {
			return err
		}
		committed = true
		return nil
	}

	if c.isShrink(reservation.Status.Allocatable, newAllocatable) {
		// Shrink short path: in-place update status.allocatable.
		// The event handler (Case 1: keep available) calls updateReservationInSchedulerCache
		// to adjust the ReservePod size in NodeInfo.
		if err := c.resizeReservationInPlace(reservation, newAllocatable); err != nil {
			return err
		}
		committed = true
		return nil
	}

	// Enlarge: create a fake resize pod and add it to the scheduler queue.
	// The reservation stays Available; the fake pod validates resources through the standard cycle.
	klog.V(3).InfoS("Reservation enlarged, creating fake resize pod for scheduler validation",
		"reservation", klog.KObj(reservation), "uid", reservation.UID,
		"node", reservation.Status.NodeName, "newAllocatable", newAllocatable)
	resizePod := c.makeResizePod(reservation, newAllocatable)
	if c.addToSchedulerQueueFn != nil {
		// Pre-enqueue safety check: verify the reservation still exists before adding the
		// fake pod to the scheduling queue. This narrows the race window where the reservation
		// is deleted between our initial Get and the enqueue, avoiding an orphaned pod in the
		// queue. The PostFilter handler provides ultimate consistency if this check is bypassed.
		if _, err := c.reservationLister.Get(reservation.Name); errors.IsNotFound(err) {
			klog.V(3).InfoS("Reservation deleted before enqueue, aborting resize",
				"reservation", klog.KObj(reservation), "uid", reservation.UID)
			return nil
		}
		c.addToSchedulerQueueFn(resizePod)
	}
	committed = true
	klog.V(3).InfoS("Successfully created fake resize pod and added to scheduler queue",
		"reservation", klog.KObj(reservation), "uid", reservation.UID,
		"resizePodUID", resizePod.UID, "node", reservation.Status.NodeName)
	return nil
}

// makeResizePod creates a fake resize pod for scheduler validation during reservation enlarge.
// The fake pod has:
//   - A NEW UID (different from the reservation UID) to avoid cache conflicts
//   - The new (enlarged) resource requirements
//   - Annotations pinning it to the reservation's current node
//   - Annotations identifying it as a resize pod and linking to the target reservation
//
// This follows the same pattern as pod inplace-update: create a fake pod with new resources,
// schedule it through the standard cycle, then apply the result to the real target.
func (c *Controller) makeResizePod(reservation *schedulingv1alpha1.Reservation, newAllocatable corev1.ResourceList) *corev1.Pod {
	resizePod := &corev1.Pod{}
	if reservation.Spec.Template != nil {
		resizePod.ObjectMeta = *reservation.Spec.Template.ObjectMeta.DeepCopy()
		resizePod.Spec = *reservation.Spec.Template.Spec.DeepCopy()
	}

	// Use a NEW UID to avoid conflicts with the existing ReservePod in scheduler cache
	resizePod.UID = uuid.NewUUID()
	resizePod.Name = string(reservation.UID) + "-resize-" + string(resizePod.UID)[:8]
	if len(resizePod.Namespace) == 0 {
		resizePod.Namespace = corev1.NamespaceDefault
	}

	// Copy labels from reservation
	if resizePod.Labels == nil {
		resizePod.Labels = map[string]string{}
	}
	for k, v := range reservation.Labels {
		resizePod.Labels[k] = v
	}

	// Set annotations
	if resizePod.Annotations == nil {
		resizePod.Annotations = map[string]string{}
	}
	for k, v := range reservation.Annotations {
		resizePod.Annotations[k] = v
	}
	// Mark as a reserve pod so existing reserve pod paths (error handler, etc.) work
	resizePod.Annotations[reservationutil.AnnotationReservePod] = "true"
	resizePod.Annotations[reservationutil.AnnotationReservationName] = reservation.Name
	// Mark as a resize pod with target reservation info
	resizePod.Annotations[reservationutil.AnnotationReservationResizePod] = "true"
	resizePod.Annotations[reservationutil.AnnotationResizeTargetReservationUID] = string(reservation.UID)
	// Pin to the current node
	resizePod.Annotations[reservationutil.AnnotationReservationNode] = reservation.Status.NodeName
	resizePod.Spec.NodeName = ""

	// Set resources to the NEW allocatable (enlarged size)
	// Clear existing containers so UpdateReservePodWithAllocatable creates a single
	// fake container with the new resources (avoids leftover empty containers from template)
	resizePod.Spec.Containers = nil
	resizePod.Spec.InitContainers = nil
	reservationutil.UpdateReservePodWithAllocatable(resizePod, nil, newAllocatable)

	// Set priority to max to prevent preemption (same as NewReservePod)
	if resizePod.Spec.Priority == nil {
		if priorityVal, ok := reservation.Labels[extension.LabelPodPriority]; ok && priorityVal != "" {
			priority, err := strconv.ParseInt(priorityVal, 10, 32)
			if err == nil {
				resizePod.Spec.Priority = ptr.To[int32](int32(priority))
			}
		}
	}
	if resizePod.Spec.Priority == nil {
		resizePod.Spec.Priority = ptr.To[int32](math.MaxInt32)
	}

	resizePod.Spec.SchedulerName = reservationutil.GetReservationSchedulerName(reservation)

	// Clear volumes to reduce overhead (not needed for resource validation)
	resizePod.Spec.Volumes = nil

	return resizePod
}

// computeNewAllocatable derives the new allocatable resources from the reservation spec template.
// When template is nil, returns an empty ResourceList (consistent with IsReservationSpecResourcesChanged
// which treats nil template as zero resources).
func (c *Controller) computeNewAllocatable(reservation *schedulingv1alpha1.Reservation) corev1.ResourceList {
	if reservation.Spec.Template == nil {
		return corev1.ResourceList{}
	}
	return resource.PodRequests(&corev1.Pod{
		Spec: reservation.Spec.Template.Spec,
	}, resource.PodResourcesOptions{})
}

// isShrink checks whether the resize is a pure shrink (all resources decrease or stay the same).
// Returns true if no resource in newAllocatable exceeds the corresponding resource in oldAllocatable.
// Resources present in old but missing from new are treated as removed (shrink to 0).
// Any resource increase means this is NOT a shrink and requires rescheduling.
func (c *Controller) isShrink(oldAllocatable, newAllocatable corev1.ResourceList) bool {
	for resourceName, newQty := range newAllocatable {
		if oldQty, ok := oldAllocatable[resourceName]; ok {
			if newQty.Cmp(oldQty) > 0 {
				return false // new > old for some resource → enlargement
			}
		} else {
			// New resource type not present in old → it's an addition → not a shrink
			return false
		}
	}
	return true
}

// resizeReservationInPlace updates the reservation's status.allocatable to the new size
// without rescheduling. The event handler will detect the status change and update
// the ReservePod in the scheduler cache (via updateReservationInSchedulerCache).
func (c *Controller) resizeReservationInPlace(reservation *schedulingv1alpha1.Reservation, newAllocatable corev1.ResourceList) error {

	// Clear any previous ResizeFailed condition since this is a new resize attempt
	clearResizeFailedCondition(reservation)

	reservation.Status.Allocatable = newAllocatable
	_, err := c.koordClientSet.SchedulingV1alpha1().Reservations().UpdateStatus(
		context.TODO(), reservation, metav1.UpdateOptions{})
	if err != nil {
		klog.ErrorS(err, "Failed to resize reservation in-place",
			"reservation", klog.KObj(reservation), "uid", reservation.UID)
		return err
	}
	klog.V(3).InfoS("Successfully resized reservation in-place",
		"reservation", klog.KObj(reservation), "uid", reservation.UID,
		"node", reservation.Status.NodeName, "newAllocatable", newAllocatable)
	return nil
}

// unbindOwnerPods removes the reservation-allocated annotation from all pods
// currently using this reservation, turning them into standalone pods.
func (c *Controller) unbindOwnerPods(reservation *schedulingv1alpha1.Reservation) error {
	pods := c.getPodsOnReservation(reservation.UID)
	if len(pods) == 0 {
		return nil
	}
	var errs []error
	for _, pod := range pods {
		curPod, err := c.podLister.Pods(pod.Namespace).Get(pod.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			errs = append(errs, err)
			continue
		}
		modifiedPod := curPod.DeepCopy()
		removed, err := apiext.RemoveReservationAllocated(modifiedPod, reservation)
		if err != nil {
			klog.V(4).ErrorS(err, "Failed to remove reservation allocated for pod during resize",
				"reservation", klog.KObj(reservation), "pod", klog.KObj(curPod))
			errs = append(errs, err)
			continue
		}
		if !removed {
			continue
		}
		_, err = util.PatchPodSafe(context.TODO(), c.client, curPod, modifiedPod)
		if err != nil {
			klog.ErrorS(err, "Failed to unbind pod from reservation for resize",
				"reservation", klog.KObj(reservation), "pod", klog.KObj(curPod))
			errs = append(errs, err)
			continue
		}
		klog.V(4).InfoS("Successfully unbound pod from reservation for resize",
			"reservation", klog.KObj(reservation), "uid", reservation.UID, "pod", klog.KObj(curPod))
	}
	return utilerrors.NewAggregate(errs)
}

// hasResizeFailedCondition checks if the reservation has a ResizeFailed condition.
func hasResizeFailedCondition(r *schedulingv1alpha1.Reservation) bool {
	for _, c := range r.Status.Conditions {
		if c.Type == schedulingv1alpha1.ReservationConditionResizeFailed &&
			c.Status == schedulingv1alpha1.ConditionStatusTrue {
			return true
		}
	}
	return false
}

// clearResizeFailedCondition removes the ResizeFailed condition from the reservation.
// This is called when a new resize attempt starts (either handleReservationResize or resizeInPlace).
func clearResizeFailedCondition(r *schedulingv1alpha1.Reservation) {
	filtered := make([]schedulingv1alpha1.ReservationCondition, 0, len(r.Status.Conditions))
	for _, c := range r.Status.Conditions {
		if c.Type != schedulingv1alpha1.ReservationConditionResizeFailed {
			filtered = append(filtered, c)
		}
	}
	r.Status.Conditions = filtered
}

// isBelowAllocated checks if any resource in newAllocatable is less than the
// corresponding resource in allocated. This is used to reject shrink operations
// that would make the reservation smaller than what's already been allocated to pods.
func isBelowAllocated(allocated, newAllocatable corev1.ResourceList) bool {
	for resourceName, allocatedQty := range allocated {
		if allocatedQty.IsZero() {
			continue
		}
		if newQty, ok := newAllocatable[resourceName]; ok {
			if newQty.Cmp(allocatedQty) < 0 {
				return true
			}
		} else {
			// Resource exists in allocated but not in new allocatable → effectively 0
			return true
		}
	}
	return false
}

// setResizeFailedCondition adds a ResizeFailed condition to the reservation.
// Any existing ResizeFailed condition is replaced.
// The Message field encodes the target allocatable that caused the failure, formatted as
// "reason;cpu=<val>,memory=<val>,...". This allows detecting spec changes: if the user
// modifies the spec to a different target, the controller clears ResizeFailed and retries.
func setResizeFailedCondition(r *schedulingv1alpha1.Reservation, reason string, targetAllocatable corev1.ResourceList) {
	clearResizeFailedCondition(r)
	now := metav1.Now()
	r.Status.Conditions = append(r.Status.Conditions, schedulingv1alpha1.ReservationCondition{
		Type:               schedulingv1alpha1.ReservationConditionResizeFailed,
		Status:             schedulingv1alpha1.ConditionStatusTrue,
		Reason:             schedulingv1alpha1.ReasonReservationResizeFailed,
		Message:            EncodeResizeFailedMessage(reason, targetAllocatable),
		LastProbeTime:      now,
		LastTransitionTime: now,
	})
}

// EncodeResizeFailedMessage builds the Message field: "reason;cpu=100m,memory=1Gi".
// The fingerprint is always appended after the LAST semicolon, so reason text
// containing semicolons (e.g. joined scheduler reasons) won't break parsing.
func EncodeResizeFailedMessage(reason string, target corev1.ResourceList) string {
	return reason + ";" + allocatableFingerprint(target)
}

// getResizeFailedTarget extracts the target fingerprint from a ResizeFailed condition's Message.
// It splits at the LAST semicolon to avoid misparse when the reason itself contains semicolons.
// Returns empty string if no semicolon or no ResizeFailed condition.
func getResizeFailedTarget(r *schedulingv1alpha1.Reservation) string {
	for _, c := range r.Status.Conditions {
		if c.Type == schedulingv1alpha1.ReservationConditionResizeFailed &&
			c.Status == schedulingv1alpha1.ConditionStatusTrue {
			if idx := strings.LastIndex(c.Message, ";"); idx >= 0 {
				return c.Message[idx+1:]
			}
			return ""
		}
	}
	return ""
}

// allocatableFingerprint returns a stable, sorted string representation of a ResourceList
// for comparison with the target stored in ResizeFailed condition.
func allocatableFingerprint(rl corev1.ResourceList) string {
	names := make([]string, 0, len(rl))
	for name := range rl {
		names = append(names, string(name))
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		qty := rl[corev1.ResourceName(name)]
		parts = append(parts, name+"="+qty.String())
	}
	return strings.Join(parts, ",")
}

func (c *Controller) syncStatus(reservation *schedulingv1alpha1.Reservation, pods map[types.UID]*corev1.Pod) error {
	if isReservationNeedExpiration(reservation) {
		return c.expireReservation(reservation)
	}

	if reservation.Status.NodeName != "" && missingNode(reservation, c.nodeLister) {
		return c.expireReservation(reservation)
	}

	if reservation.Status.NodeName == "" {
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
		requests := componentresource.PodRequests(pod, componentresource.PodResourcesOptions{})
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

const (
	defaultResyncInterval = 60 * time.Second
)

func (c *Controller) resyncReservations() {
	reservations, err := c.reservationLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list reservations, abort the resync turn, err: %s", err)
		return
	}
	metrics.ResetReservationPhase()
	for _, reservation := range reservations {
		// record metrics
		RecordReservationPhases(reservation)
	}
	klog.V(4).InfoS("resynced reservation metrics", "count", len(reservations))
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
