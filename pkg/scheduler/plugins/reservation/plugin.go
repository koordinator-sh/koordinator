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

package reservation

import (
	"context"
	"fmt"
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	corev1helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	clientschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/scheduling/v1alpha1"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	Name     = "Reservation"
	stateKey = Name

	// ErrReasonNodeNotMatchReservation is the reason for node not matching which the reserve pod specifies.
	ErrReasonNodeNotMatchReservation = "node(s) didn't match the nodeName specified by reservation"
	// ErrReasonReservationInactive is the reason for the reservation is failed/succeeded and should not be used.
	ErrReasonReservationInactive = "reservation is not active"
)

var (
	_ framework.PreFilterPlugin  = &Plugin{}
	_ framework.FilterPlugin     = &Plugin{}
	_ framework.PostFilterPlugin = &Plugin{}
	_ framework.ScorePlugin      = &Plugin{}
	_ framework.ReservePlugin    = &Plugin{}
	_ framework.PreBindPlugin    = &Plugin{}
	_ framework.BindPlugin       = &Plugin{}

	_ frameworkext.PreFilterTransformer   = &Plugin{}
	_ frameworkext.ReservationRecommender = &Plugin{}
	_ frameworkext.ReservationScorePlugin = &Plugin{}
)

// for internal interface testing
type parallelizeUntilFunc func(ctx context.Context, pieces int, doWorkPiece workqueue.DoWorkPieceFunc)

type Plugin struct {
	handle           frameworkext.ExtendedHandle
	args             *config.ReservationArgs
	rLister          listerschedulingv1alpha1.ReservationLister
	client           clientschedulingv1alpha1.SchedulingV1alpha1Interface // for updates
	parallelizeUntil parallelizeUntilFunc
	reservationCache *reservationCache
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*config.ReservationArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type ReservationArgs, got %T", args)
	}
	extendedHandle, ok := handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, fmt.Errorf("want handle to be of type frameworkext.ExtendedHandle, got %T", handle)
	}

	koordSharedInformerFactory := extendedHandle.KoordinatorSharedInformerFactory()
	reservationInterface := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations()
	reservationMgr := newReservationCache(extendedHandle.SharedInformerFactory(), koordSharedInformerFactory)

	p := &Plugin{
		handle:           extendedHandle,
		args:             pluginArgs,
		rLister:          reservationInterface.Lister(),
		client:           extendedHandle.KoordinatorClientSet().SchedulingV1alpha1(),
		parallelizeUntil: handle.Parallelizer().Until,
		reservationCache: reservationMgr,
	}

	// handle reservations on deleted nodes
	nodeInformer := extendedHandle.SharedInformerFactory().Core().V1().Nodes().Informer()
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), extendedHandle.SharedInformerFactory(), nodeInformer, cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			switch t := obj.(type) {
			case *corev1.Node:
				p.expireReservationOnNode(t)
				return
			case cache.DeletedFinalStateUnknown:
				node, ok := t.Obj.(*corev1.Node)
				if ok && node != nil {
					p.expireReservationOnNode(node)
					return
				}
			}
			klog.V(3).InfoS("reservation's node informer delete func parse obj failed", "obj", obj)
		},
	})
	// handle deleting pods which allocate reservation resources
	// FIXME: the handler does not recognize if the pod belongs to current scheduler, so the reconciliation could be
	//  duplicated if multiple koord-scheduler runs in same cluster. Now we should keep the reconciliation idempotent.
	podInformer := extendedHandle.SharedInformerFactory().Core().V1().Pods().Informer()
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), extendedHandle.SharedInformerFactory(), podInformer, cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			switch t := obj.(type) {
			case *corev1.Pod:
				p.syncPodDeleted(t)
				return
			case cache.DeletedFinalStateUnknown:
				pod, ok := t.Obj.(*corev1.Pod)
				if ok && pod != nil {
					p.syncPodDeleted(pod)
					return
				}
			}
			klog.V(3).InfoS("reservation's pod informer delete func parse obj failed", "obj", obj)
		},
	})

	// check reservations' expiration
	go wait.Until(p.gcReservations, defaultGCCheckInterval, nil)

	return p, nil
}

func (p *Plugin) Name() string { return Name }

var _ framework.StateData = &stateData{}

type stateData struct {
	matched       map[string][]*reservationInfo
	unmatched     map[string][]*reservationInfo
	preferredNode string
	assumed       *schedulingv1alpha1.Reservation
}

func (s *stateData) Clone() framework.StateData {
	return s
}

func getStateData(cycleState *framework.CycleState) *stateData {
	v, err := cycleState.Read(stateKey)
	if err != nil {
		return &stateData{}
	}
	s, ok := v.(*stateData)
	if !ok || s == nil {
		return &stateData{}
	}
	return s
}

// PreFilter checks if the pod is a reserve pod. If it is, update cycle state to annotate reservation scheduling.
// Also do validations in this phase.
func (p *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	if reservationutil.IsReservePod(pod) {
		// validate reserve pod and reservation
		klog.V(4).InfoS("Attempting to pre-filter reserve pod", "pod", klog.KObj(pod))
		rName := reservationutil.GetReservationNameFromReservePod(pod)
		r, err := p.rLister.Get(rName)
		if err != nil {
			if errors.IsNotFound(err) {
				klog.V(3).InfoS("skip the pre-filter for reservation since the object is not found", "pod", klog.KObj(pod), "reservation", rName)
			}
			return framework.NewStatus(framework.Error, "cannot get reservation, err: "+err.Error())
		}
		err = reservationutil.ValidateReservation(r)
		if err != nil {
			return framework.NewStatus(framework.Error, err.Error())
		}
		return nil
	}

	klog.V(5).InfoS("Attempting to pre-filter pod for reservation state", "pod", klog.KObj(pod))
	return nil
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// Filter only processes pods either the pod is a reserve pod or a pod can allocate reserved resources on the node.
func (p *Plugin) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	if reservationutil.IsReservePod(pod) {
		klog.V(4).InfoS("Attempting to filter reserve pod", "pod", klog.KObj(pod), "node", node.Name)
		// if the reservation specifies a nodeName initially, check if the nodeName matches
		rNodeName := reservationutil.GetReservePodNodeName(pod)
		if len(rNodeName) > 0 && rNodeName != nodeInfo.Node().Name {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonNodeNotMatchReservation)
		}
		// TODO: handle pre-allocation cases

		return nil
	}

	return nil
}

func (p *Plugin) PostFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, filteredNodeStatusMap framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	if reservationutil.IsReservePod(pod) {
		// return err to stop default preemption
		return nil, framework.NewStatus(framework.Error)
	}

	allNodes, err := p.handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	p.parallelizeUntil(ctx, len(allNodes), func(piece int) {
		nodeInfo := allNodes[piece]
		node := nodeInfo.Node()
		if node == nil {
			return
		}
		reservationInfos := p.reservationCache.listReservationInfosOnNode(node.Name)
		for _, rInfo := range reservationInfos {
			newReservePod := reservationutil.NewReservePod(rInfo.reservation)
			if corev1helpers.PodPriority(newReservePod) < corev1helpers.PodPriority(pod) {
				maxPri := int32(math.MaxInt32)
				if err := nodeInfo.RemovePod(newReservePod); err == nil {
					newReservePod.Spec.Priority = &maxPri
					nodeInfo.AddPod(newReservePod)
					// NOTE: To achieve incremental update with frameworkext.TemporarySnapshot, you need to set Generation to -1
					nodeInfo.Generation = -1
				}
			}
		}
	})
	return nil, framework.NewStatus(framework.Unschedulable)
}

func (p *Plugin) Reserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	if reservationutil.IsReservePod(pod) {
		return nil
	}

	recommendReservation := frameworkext.GetRecommendReservation(cycleState)
	if recommendReservation == nil {
		klog.V(5).Infof("Skip reserve with reservation since there are no matched reservations, pod %v, node: %v", klog.KObj(pod), nodeName)
		return nil
	}

	// NOTE: Having entered the Reserve stage means that the Pod scheduling is successful,
	// even though the associated Reservation may have expired, but in fact the real impact
	// will not be encountered until the next round of scheduling.
	assumed := recommendReservation.DeepCopy()
	p.reservationCache.assumePod(assumed.UID, pod)

	state := getStateData(cycleState)
	state.assumed = assumed
	klog.V(4).InfoS("Reserve pod to node with reservations", "pod", klog.KObj(pod), "node", nodeName, "assumed", klog.KObj(assumed))
	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) {
	if reservationutil.IsReservePod(pod) {
		return
	}

	state := getStateData(cycleState)
	if state.assumed != nil {
		klog.V(4).InfoS("Attempting to unreserve pod to node with reservations", "pod", klog.KObj(pod), "node", nodeName, "assumed", klog.KObj(state.assumed))
		p.reservationCache.forgetPod(state.assumed.UID, pod)
	} else {
		klog.V(5).InfoS("skip the Reservation Unreserve, no assumed reservation", "pod", klog.KObj(pod), "node", nodeName)
	}
	return
}

func (p *Plugin) PreBind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	if reservationutil.IsReservePod(pod) {
		return nil
	}

	state := getStateData(cycleState)
	if state.assumed == nil {
		klog.V(5).Infof("Skip the Reservation PreBind since no reservation allocated for the pod %d o node %s", klog.KObj(pod), nodeName)
		return nil
	}

	reservation := state.assumed
	klog.V(4).Infof("Attempting to pre-bind pod %v to node %d with reservation %v", klog.KObj(pod), nodeName, klog.KObj(reservation))

	newPod := pod.DeepCopy()
	apiext.SetReservationAllocated(newPod, reservation)
	err := util.RetryOnConflictOrTooManyRequests(func() error {
		_, err := util.NewPatch().WithClientset(p.handle.ClientSet()).AddAnnotations(newPod.Annotations).PatchPod(ctx, pod)
		return err
	})
	if err != nil {
		klog.V(4).ErrorS(err, "failed to patch pod for PreBind allocating reservation", "pod", klog.KObj(pod))
		return framework.AsStatus(err)
	}
	klog.V(4).Infof("Successfully preBind pod %v with reservation %v on node %s", klog.KObj(pod), klog.KObj(reservation), nodeName)
	return nil
}

// Bind fake binds reserve pod and mark corresponding reservation as Available.
// NOTE: This Bind plugin should get called before DefaultBinder; plugin order should be configured.
func (p *Plugin) Bind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	if !reservationutil.IsReservePod(pod) {
		return framework.NewStatus(framework.Skip)
	}

	rName := reservationutil.GetReservationNameFromReservePod(pod)
	klog.V(4).InfoS("Attempting to fake bind reserve pod to node",
		"pod", klog.KObj(pod), "reservation", rName, "node", nodeName)

	var reservation *schedulingv1alpha1.Reservation
	err := util.RetryOnConflictOrTooManyRequests(func() error {
		var err error
		reservation, err = p.rLister.Get(rName)
		if err != nil {
			return err
		}

		// check if the reservation has been inactive
		if reservationutil.IsReservationFailed(reservation) {
			return fmt.Errorf(ErrReasonReservationInactive)
		}

		// mark reservation as available
		reservation = reservation.DeepCopy()
		setReservationAvailable(reservation, nodeName)
		_, err = p.client.Reservations().UpdateStatus(context.TODO(), reservation, metav1.UpdateOptions{})
		if err != nil {
			klog.V(4).ErrorS(err, "failed to update reservation", "reservation", klog.KObj(reservation))
		}
		return err
	})
	if err != nil {
		klog.Errorf("Failed to update bind Reservation %s, err: %v", rName, err)
		return framework.AsStatus(err)
	}

	p.handle.EventRecorder().Eventf(reservation, nil, corev1.EventTypeNormal, "Scheduled", "Binding", "Successfully assigned %v to %v", rName, nodeName)
	return nil
}
