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
	fwktype "k8s.io/kube-scheduler/framework"
	pluginhelper "k8s.io/kubernetes/pkg/scheduler/framework/plugins/helper"

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
	args              *config.CoschedulingArgs
	frameworkHandler  fwktype.Handle
	pgClient          pgclientset.Interface
	pgInformerFactory pgformers.SharedInformerFactory
	pgInformer        schedinformers.PodGroupInformer
	pgMgr             core.Manager
}

var _ fwktype.PreEnqueuePlugin = &Coscheduling{}
var _ frameworkext.NextPodPlugin = &Coscheduling{}
var _ frameworkext.PreFilterTransformer = &Coscheduling{}
var _ fwktype.PreFilterPlugin = &Coscheduling{}
var _ frameworkext.FindOneNodePluginProvider = &Coscheduling{}
var _ frameworkext.FindOneNodePlugin = &Coscheduling{}
var _ frameworkext.PostFilterTransformer = &Coscheduling{}
var _ fwktype.PostFilterPlugin = &Coscheduling{}
var _ fwktype.PreScorePlugin = &Coscheduling{}
var _ fwktype.ScorePlugin = &Coscheduling{}
var _ fwktype.PermitPlugin = &Coscheduling{}
var _ fwktype.ReservePlugin = &Coscheduling{}
var _ fwktype.PreBindPlugin = &Coscheduling{}
var _ frameworkext.ReservationPreBindPlugin = &Coscheduling{}
var _ fwktype.PostBindPlugin = &Coscheduling{}
var _ fwktype.EnqueueExtensions = &Coscheduling{}
var _ frameworkext.InformerFactoryProvider = &Coscheduling{}

const (
	// Name is the name of the plugin used in Registry and configurations.
	Name = core.Name
)

// New initializes and returns a new Coscheduling plugin.
func New(_ context.Context, obj runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
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
		args:              args,
		frameworkHandler:  handle,
		pgClient:          pgClient,
		pgInformerFactory: pgInformerFactory,
		pgInformer:        pgInformer,
		pgMgr:             pgMgr,
	}
	return plugin, nil
}

func (cs *Coscheduling) EventsToRegister(_ context.Context) ([]fwktype.ClusterEventWithHint, error) {
	// Coscheduling plugin does not benefit from QueueingHints as gang pods are managed by the controller
	// and re-queued together. Returning nil indicates we don't register any custom events for re-queuing individual pods.
	return nil, nil
}

// Name returns name of the plugin. It is used in logs, etc.
func (cs *Coscheduling) Name() string {
	return Name
}

// PreEnqueue
// i.Check whether childes in Gang has met the requirements of minimum number under each Gang, and reject the pod if negative.
// ii.Check whether the Gang has been timeout(check the pod's annotation,later introduced at Permit section) or is inited, and reject the pod if positive.
func (cs *Coscheduling) PreEnqueue(ctx context.Context, pod *v1.Pod) *fwktype.Status {
	if err := cs.pgMgr.PreEnqueue(ctx, pod); err != nil {
		klog.ErrorS(err, "PreEnqueue failed", "pod", klog.KObj(pod))
		return fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, err.Error())
	}
	return fwktype.NewStatus(fwktype.Success, "")
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

func (cs *Coscheduling) FindOneNode(ctx context.Context, cycleState fwktype.CycleState, pod *v1.Pod, result *fwktype.PreFilterResult) (string, *fwktype.Status) {
	return cs.pgMgr.FindOneNode(ctx, cycleState, pod, result)
}

// BeforePreFilter
// i.Check whether the Gang has met the scheduleCycleValid check, and reject the pod if negative.
// ii.Try update scheduleCycle, scheduleCycleValid, childrenScheduleRoundMap as mentioned above.
func (cs *Coscheduling) BeforePreFilter(ctx context.Context, state fwktype.CycleState, pod *v1.Pod) (*v1.Pod, bool, *fwktype.Status) {
	// If PreFilter fails, return fwktype.UnschedulableAndUnresolvable to avoid any preemption attempts.
	if err := cs.pgMgr.BeforePreFilter(ctx, state, pod); err != nil {
		klog.ErrorS(err, "PreFilter failed", "pod", klog.KObj(pod))
		return nil, false, fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, err.Error())
	}
	return nil, false, fwktype.NewStatus(fwktype.Success, "")
}

func (cs *Coscheduling) PreFilter(ctx context.Context, state fwktype.CycleState, pod *v1.Pod, nodes []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	return cs.pgMgr.PreFilter(ctx, state, pod, nodes)
}

func (cs *Coscheduling) AfterPreFilter(ctx context.Context, cycleState fwktype.CycleState, pod *v1.Pod, preFilterResult *fwktype.PreFilterResult) *fwktype.Status {
	return nil
}

func (cs *Coscheduling) AfterPostFilter(ctx context.Context, state fwktype.CycleState, pod *v1.Pod, filteredNodeStatusMap fwktype.NodeToStatusReader) {
	cs.pgMgr.AfterPostFilter(ctx, state, pod, cs.frameworkHandler, Name, filteredNodeStatusMap)
}

// PostFilter
// i. If strict-mode, we will set scheduleCycleValid to false and release all assumed pods.
// ii. If non-strict mode, we will do nothing.
func (cs *Coscheduling) PostFilter(ctx context.Context, state fwktype.CycleState, pod *v1.Pod, filteredNodeStatusMap fwktype.NodeToStatusReader) (*fwktype.PostFilterResult, *fwktype.Status) {
	return cs.pgMgr.PostFilter(ctx, state, pod, filteredNodeStatusMap)
}

// PreFilterExtensions returns a PreFilterExtensions interface if the plugin implements one.
func (cs *Coscheduling) PreFilterExtensions() fwktype.PreFilterExtensions {
	return nil
}

func (cs *Coscheduling) PreScore(ctx context.Context, state fwktype.CycleState, pod *v1.Pod, nodes []fwktype.NodeInfo) *fwktype.Status {
	return cs.pgMgr.PreScore(ctx, state, pod, nodes)
}

func (cs *Coscheduling) Score(ctx context.Context, state fwktype.CycleState, p *v1.Pod, nodeInfo fwktype.NodeInfo) (int64, *fwktype.Status) {
	return cs.pgMgr.Score(ctx, state, p, nodeInfo)
}

func (cs *Coscheduling) ScoreExtensions() fwktype.ScoreExtensions {
	return cs
}

func (cs *Coscheduling) NormalizeScore(ctx context.Context, state fwktype.CycleState, p *v1.Pod, scores fwktype.NodeScoreList) *fwktype.Status {
	return pluginhelper.DefaultNormalizeScore(fwktype.MaxNodeScore, true, scores)
}

// Permit
// we will calculate all Gangs in GangGroup whether the current number of assumed-pods in each Gang meets the Gang's minimum requirement.
// and decide whether we should let the pod wait in Permit stage or let the whole gangGroup go binding
func (cs *Coscheduling) Permit(ctx context.Context, state fwktype.CycleState, pod *v1.Pod, nodeName string) (*fwktype.Status, time.Duration) {
	waitTime, s := cs.pgMgr.Permit(ctx, pod)
	var retStatus *fwktype.Status
	switch s {
	case core.PodGroupNotSpecified:
		return fwktype.NewStatus(fwktype.Success, ""), 0
	case core.PodGroupNotFound:
		return fwktype.NewStatus(fwktype.Unschedulable, "Gang not found"), 0
	case core.Wait:
		klog.InfoS("Pod is waiting to be scheduled at Permit stage", "gang",
			util.GetId(pod.Namespace, util.GetGangNameByPod(pod)), "pod", klog.KObj(pod))
		retStatus = fwktype.NewStatus(fwktype.Wait)
	case core.Success:
		cs.pgMgr.AllowGangGroup(pod, cs.frameworkHandler, Name)
		cs.pgMgr.SucceedGangScheduling()
		retStatus = fwktype.NewStatus(fwktype.Success)
		waitTime = 0
	}
	return retStatus, waitTime
}

// Reserve is the functions invoked by the framework at "reserve" extension point.
func (cs *Coscheduling) Reserve(ctx context.Context, state fwktype.CycleState, pod *v1.Pod, nodeName string) *fwktype.Status {
	return nil
}

// Unreserve
// i. handle the timeout gang
// ii. do nothing when bound failed
func (cs *Coscheduling) Unreserve(ctx context.Context, state fwktype.CycleState, pod *v1.Pod, nodeName string) {
	cs.pgMgr.Unreserve(ctx, state, pod, nodeName, cs.frameworkHandler, Name)
}

func (cs *Coscheduling) PreBindPreFlight(ctx context.Context, cycleState fwktype.CycleState, pod *v1.Pod, nodeName string) *fwktype.Status {
	return nil
}

func (cs *Coscheduling) PreBind(ctx context.Context, cycleState fwktype.CycleState, pod *v1.Pod, nodeName string) *fwktype.Status {
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

func (cs *Coscheduling) PreBindReservation(ctx context.Context, cycleState fwktype.CycleState, r *schedulingv1alpha1.Reservation, nodeName string) *fwktype.Status {
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
func (cs *Coscheduling) PostBind(ctx context.Context, _ fwktype.CycleState, pod *v1.Pod, nodeName string) {
	cs.pgMgr.PostBind(ctx, pod, nodeName)
}

// GetInformerFactories returns the PodGroup informer factory for central startup management.
func (cs *Coscheduling) GetInformerFactories() []frameworkext.SharedInformerFactory {
	return []frameworkext.SharedInformerFactory{cs.pgInformerFactory}
}
