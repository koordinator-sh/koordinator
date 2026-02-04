/*
Copyright 2022 The Koordinator Authors.
Copyright 2020 The Kubernetes Authors.

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

package coscheduling

import (
	"context"
	"fmt"
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	pgclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"
	pgformers "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/informers/externalversions"
	schedinformers "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/informers/externalversions/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/core"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// Coscheduling is a plugin that schedules pods in a group.
type Coscheduling struct {
	args             *config.CoschedulingArgs
	frameworkHandler framework.Handle
	pgClient         pgclientset.Interface
	pgInformer       schedinformers.PodGroupInformer
	pgMgr            core.Manager
}

var _ framework.PreEnqueuePlugin = &Coscheduling{}
var _ frameworkext.NextPodPlugin = &Coscheduling{}
var _ frameworkext.PreFilterTransformer = &Coscheduling{}
var _ framework.PreFilterPlugin = &Coscheduling{}
var _ frameworkext.FindOneNodePluginProvider = &Coscheduling{}
var _ frameworkext.FindOneNodePlugin = &Coscheduling{}
var _ frameworkext.PostFilterTransformer = &Coscheduling{}
var _ framework.PostFilterPlugin = &Coscheduling{}
var _ framework.PermitPlugin = &Coscheduling{}
var _ framework.ReservePlugin = &Coscheduling{}
var _ framework.PreBindPlugin = &Coscheduling{}
var _ frameworkext.ReservationPreBindPlugin = &Coscheduling{}
var _ framework.PostBindPlugin = &Coscheduling{}
var _ framework.EnqueueExtensions = &Coscheduling{}

const (
	// Name is the name of the plugin used in Registry and configurations.
	Name = core.Name
)

// New initializes and returns a new Coscheduling plugin.
func New(obj runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	args, ok := obj.(*config.CoschedulingArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type CoschedulingArgs, got %T", obj)
	}
	if err := validation.ValidateCoschedulingArgs(args); err != nil {
		return nil, err
	}
	pgClient, ok := handle.(pgclientset.Interface)
	if !ok {
		kubeConfig := *handle.KubeConfig()
		kubeConfig.ContentType = runtime.ContentTypeJSON
		kubeConfig.AcceptContentTypes = runtime.ContentTypeJSON
		pgClient = pgclientset.NewForConfigOrDie(&kubeConfig)
	}
	pgInformerFactory := pgformers.NewSharedInformerFactory(pgClient, 0)
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()

	informerFactory := handle.SharedInformerFactory()
	extendedHandle := handle.(frameworkext.ExtendedHandle)
	koordInformerFactory := extendedHandle.KoordinatorSharedInformerFactory()
	pgMgr := core.NewPodGroupManager(handle, args, pgClient, pgInformerFactory, informerFactory, koordInformerFactory)
	plugin := &Coscheduling{
		args:             args,
		frameworkHandler: handle,
		pgClient:         pgClient,
		pgInformer:       pgInformer,
		pgMgr:            pgMgr,
	}
	return plugin, nil
}

func (cs *Coscheduling) EventsToRegister() []framework.ClusterEventWithHint {
	// indicates that we are not interested in any events
	return nil
}

// Name returns name of the plugin. It is used in logs, etc.
func (cs *Coscheduling) Name() string {
	return Name
}

// PreEnqueue
// i.Check whether childes in Gang has met the requirements of minimum number under each Gang, and reject the pod if negative.
// ii.Check whether the Gang has been timeout(check the pod's annotation,later introduced at Permit section) or is inited, and reject the pod if positive.
func (cs *Coscheduling) PreEnqueue(ctx context.Context, pod *v1.Pod) *framework.Status {
	if err := cs.pgMgr.PreEnqueue(ctx, pod); err != nil {
		klog.ErrorS(err, "PreEnqueue failed", "pod", klog.KObj(pod))
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}
	return framework.NewStatus(framework.Success, "")
}

func (cs *Coscheduling) NextPod() *v1.Pod {
	return cs.pgMgr.NextPod()
}

func (cs *Coscheduling) FindOneNodePlugin() frameworkext.FindOneNodePlugin {
	if cs.args != nil && *cs.args.AwareNetworkTopology {
		return cs
	}
	return nil
}

func (cs *Coscheduling) FindOneNode(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, result *framework.PreFilterResult) (string, *framework.Status) {
	return cs.pgMgr.FindOneNode(ctx, cycleState, pod, result)
}

// BeforePreFilter
// i.Check whether the Gang has met the scheduleCycleValid check, and reject the pod if negative.
// ii.Try update scheduleCycle, scheduleCycleValid, childrenScheduleRoundMap as mentioned above.
func (cs *Coscheduling) BeforePreFilter(ctx context.Context, state *framework.CycleState, pod *v1.Pod) (*v1.Pod, bool, *framework.Status) {
	// If PreFilter fails, return framework.UnschedulableAndUnresolvable to avoid any preemption attempts.
	if err := cs.pgMgr.BeforePreFilter(ctx, state, pod); err != nil {
		klog.ErrorS(err, "PreFilter failed", "pod", klog.KObj(pod))
		return nil, false, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}
	return nil, false, framework.NewStatus(framework.Success, "")
}

func (cs *Coscheduling) PreFilter(ctx context.Context, state *framework.CycleState, pod *v1.Pod) (*framework.PreFilterResult, *framework.Status) {
	return cs.pgMgr.PreFilter(ctx, state, pod)
}

func (cs *Coscheduling) AfterPreFilter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, preFilterResult *framework.PreFilterResult) *framework.Status {
	return nil
}

func (cs *Coscheduling) AfterPostFilter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, filteredNodeStatusMap framework.NodeToStatusMap) {
	cs.pgMgr.AfterPostFilter(ctx, state, pod, cs.frameworkHandler, Name, filteredNodeStatusMap)
}

// PostFilter
// i. If strict-mode, we will set scheduleCycleValid to false and release all assumed pods.
// ii. If non-strict mode, we will do nothing.
func (cs *Coscheduling) PostFilter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, filteredNodeStatusMap framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	return cs.pgMgr.PostFilter(ctx, state, pod, filteredNodeStatusMap)
}

// PreFilterExtensions returns a PreFilterExtensions interface if the plugin implements one.
func (cs *Coscheduling) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// Permit
// we will calculate all Gangs in GangGroup whether the current number of assumed-pods in each Gang meets the Gang's minimum requirement.
// and decide whether we should let the pod wait in Permit stage or let the whole gangGroup go binding
func (cs *Coscheduling) Permit(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (*framework.Status, time.Duration) {
	waitTime, s := cs.pgMgr.Permit(ctx, pod)
	var retStatus *framework.Status
	switch s {
	case core.PodGroupNotSpecified:
		return framework.NewStatus(framework.Success, ""), 0
	case core.PodGroupNotFound:
		return framework.NewStatus(framework.Unschedulable, "Gang not found"), 0
	case core.Wait:
		klog.InfoS("Pod is waiting to be scheduled at Permit stage", "gang",
			util.GetId(pod.Namespace, util.GetGangNameByPod(pod)), "pod", klog.KObj(pod))
		retStatus = framework.NewStatus(framework.Wait)
	case core.Success:
		cs.pgMgr.AllowGangGroup(pod, cs.frameworkHandler, Name)
		cs.pgMgr.SucceedGangScheduling()
		retStatus = framework.NewStatus(framework.Success)
		waitTime = 0
	}
	return retStatus, waitTime
}

// Reserve is the functions invoked by the framework at "reserve" extension point.
func (cs *Coscheduling) Reserve(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	return nil
}

// Unreserve
// i. handle the timeout gang
// ii. do nothing when bound failed
func (cs *Coscheduling) Unreserve(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) {
	cs.pgMgr.Unreserve(ctx, state, pod, nodeName, cs.frameworkHandler, Name)
}

func (cs *Coscheduling) PreBind(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	gangInfo := cs.pgMgr.GetGangBindingInfo(pod)
	if gangInfo == nil {
		delete(pod.Annotations, extension.AnnotationBindGangGroupId)
		delete(pod.Annotations, extension.AnnotationBindGangMemberCount)
		// DEPRECATED: This api is marked as internal and will be removed next version.
		delete(pod.Annotations, extension.DeprecatedAnnotationBindGangGroupId)
		delete(pod.Annotations, extension.DeprecatedAnnotationBindGangMemberCount)
		return nil
	}
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[extension.AnnotationBindGangGroupId] = gangInfo.GangGroupId
	pod.Annotations[extension.AnnotationBindGangMemberCount] = strconv.FormatInt(int64(gangInfo.MemberCount), 10)
	// DEPRECATED: This api is marked as internal and will be removed next version.
	pod.Annotations[extension.DeprecatedAnnotationBindGangGroupId] = gangInfo.GangGroupId
	pod.Annotations[extension.DeprecatedAnnotationBindGangMemberCount] = strconv.FormatInt(int64(gangInfo.MemberCount), 10)
	return nil
}

func (cs *Coscheduling) PreBindReservation(ctx context.Context, cycleState *framework.CycleState, r *schedulingv1alpha1.Reservation, nodeName string) *framework.Status {
	pod := reservationutil.NewReservePod(r)
	gangInfo := cs.pgMgr.GetGangBindingInfo(pod)
	if gangInfo == nil {
		delete(r.Annotations, extension.AnnotationBindGangGroupId)
		delete(r.Annotations, extension.AnnotationBindGangMemberCount)
		// DEPRECATED: This api is marked as internal and will be removed next version.
		delete(r.Annotations, extension.DeprecatedAnnotationBindGangGroupId)
		delete(r.Annotations, extension.DeprecatedAnnotationBindGangMemberCount)
		return nil
	}
	if r.Annotations == nil {
		r.Annotations = make(map[string]string)
	}
	r.Annotations[extension.AnnotationBindGangGroupId] = gangInfo.GangGroupId
	r.Annotations[extension.AnnotationBindGangMemberCount] = strconv.FormatInt(int64(gangInfo.MemberCount), 10)
	// DEPRECATED: This api is marked as internal and will be removed next version.
	r.Annotations[extension.DeprecatedAnnotationBindGangGroupId] = gangInfo.GangGroupId
	r.Annotations[extension.DeprecatedAnnotationBindGangMemberCount] = strconv.FormatInt(int64(gangInfo.MemberCount), 10)
	return nil
}

// PostBind is called after a pod is successfully bound. These plugins are used update PodGroup when pod is bound.
func (cs *Coscheduling) PostBind(ctx context.Context, _ *framework.CycleState, pod *v1.Pod, nodeName string) {
	cs.pgMgr.PostBind(ctx, pod, nodeName)
}
