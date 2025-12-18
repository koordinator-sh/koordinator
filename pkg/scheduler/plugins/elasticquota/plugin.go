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

package elasticquota

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	v1 "k8s.io/client-go/listers/core/v1"
	policylisters "k8s.io/client-go/listers/policy/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/preemption"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling"
	apiv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/informers/externalversions"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
	"github.com/koordinator-sh/koordinator/pkg/util/transformer"
)

const (
	Name                              = "ElasticQuota"
	MigrateDefaultQuotaGroupsPodCycle = 1 * time.Second
	postFilterKey                     = "PostFilter" + Name
)

type PostFilterState struct {
	skip               bool
	quotaInfo          *core.QuotaInfo
	used               corev1.ResourceList
	nonPreemptibleUsed corev1.ResourceList
	usedLimit          corev1.ResourceList
}

func (p *PostFilterState) Clone() framework.StateData {
	return &PostFilterState{
		skip:               p.skip,
		quotaInfo:          p.quotaInfo,
		used:               p.used.DeepCopy(),
		nonPreemptibleUsed: p.nonPreemptibleUsed.DeepCopy(),
		usedLimit:          p.usedLimit.DeepCopy(),
	}
}

type Plugin struct {
	handle            framework.Handle
	client            versioned.Interface
	pluginArgs        *config.ElasticQuotaArgs
	quotaLister       v1alpha1.ElasticQuotaLister
	quotaInformer     cache.SharedIndexInformer
	podLister         v1.PodLister
	pdbLister         policylisters.PodDisruptionBudgetLister
	nodeLister        v1.NodeLister
	groupQuotaManager *core.GroupQuotaManager

	quotaManagerLock sync.RWMutex
	// groupQuotaManagersForQuotaTree store the GroupQuotaManager of all quota trees. The key is the quota tree id
	groupQuotaManagersForQuotaTree map[string]*core.GroupQuotaManager

	quotaToTreeMapLock sync.RWMutex
	// quotaToTreeMap store the relationship of quota and quota tree
	// the key is the quota name, the value is the tree id
	quotaToTreeMap map[string]string

	// quotaSnapshot stores the quota snapshot for each quota tree
	// The key is tree ID, the value is the snapshot
	// This snapshot is updated periodically in background and doesn't need to stay in sync with quotaInfoMap
	quotaSnapshotLock sync.RWMutex
	quotaSnapshot     map[string]*core.QuotaSnapshot

	// quotaToTreeMapSnapshot stores a snapshot of quotaToTreeMap
	// This snapshot is updated periodically in background and doesn't need to stay in sync with quotaToTreeMap
	quotaToTreeMapSnapshotLock sync.RWMutex
	quotaToTreeMapSnapshot     map[string]string
}

var (
	_ framework.EnqueueExtensions = &Plugin{}
	_ framework.PreFilterPlugin   = &Plugin{}
	_ framework.PostFilterPlugin  = &Plugin{}
	_ framework.ReservePlugin     = &Plugin{}
)

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*config.ElasticQuotaArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type ElasticQuotaArgs, got %T", args)
	}
	if err := validation.ValidateElasticQuotaArgs(pluginArgs); err != nil {
		return nil, err
	}

	client, ok := handle.(versioned.Interface)
	if !ok {
		kubeConfig := *handle.KubeConfig()
		kubeConfig.ContentType = runtime.ContentTypeJSON
		kubeConfig.AcceptContentTypes = runtime.ContentTypeJSON
		client = versioned.NewForConfigOrDie(&kubeConfig)
	}

	scheSharedInformerFactory := externalversions.NewSharedInformerFactory(client, 0)
	transformer.SetupElasticQuotaTransformers(scheSharedInformerFactory)
	elasticQuotaInformer := scheSharedInformerFactory.Scheduling().V1alpha1().ElasticQuotas()
	informer := elasticQuotaInformer.Informer()
	if err := informer.AddIndexers(map[string]cache.IndexFunc{
		"annotation.namespaces": func(obj interface{}) ([]string, error) {
			eq, ok := obj.(*apiv1alpha1.ElasticQuota)
			if !ok {
				return []string{}, nil
			}
			if len(eq.Annotations) == 0 || eq.Annotations[extension.AnnotationQuotaNamespaces] == "" {
				return []string{}, nil
			}
			return extension.GetAnnotationQuotaNamespaces(eq), nil
		},
	}); err != nil {
		return nil, err
	}

	elasticQuota := &Plugin{
		handle:                         handle,
		client:                         client,
		pluginArgs:                     pluginArgs,
		podLister:                      handle.SharedInformerFactory().Core().V1().Pods().Lister(),
		quotaInformer:                  informer,
		quotaLister:                    elasticQuotaInformer.Lister(),
		pdbLister:                      getPDBLister(handle),
		nodeLister:                     handle.SharedInformerFactory().Core().V1().Nodes().Lister(),
		groupQuotaManagersForQuotaTree: make(map[string]*core.GroupQuotaManager),
		quotaToTreeMap:                 make(map[string]string),
		quotaSnapshot:                  make(map[string]*core.QuotaSnapshot),
		quotaToTreeMapSnapshot:         make(map[string]string),
	}
	elasticQuota.groupQuotaManager = core.NewGroupQuotaManager("", pluginArgs.EnableMinQuotaScale, pluginArgs.SystemQuotaGroupMax,
		pluginArgs.DefaultQuotaGroupMax)
	err := elasticQuota.groupQuotaManager.InitHookPlugins(pluginArgs)
	if err != nil {
		return nil, err
	}

	elasticQuota.quotaToTreeMap[extension.DefaultQuotaName] = ""
	elasticQuota.quotaToTreeMap[extension.SystemQuotaName] = ""

	ctx := context.TODO()

	elasticQuota.createRootQuotaIfNotPresent()
	elasticQuota.createSystemQuotaIfNotPresent()
	elasticQuota.createDefaultQuotaIfNotPresent()
	_, err = frameworkexthelper.ForceSyncFromInformerWithReplace(ctx.Done(), scheSharedInformerFactory, informer, cache.ResourceEventHandlerFuncs{
		AddFunc:    elasticQuota.OnQuotaAdd,
		UpdateFunc: elasticQuota.OnQuotaUpdate,
		DeleteFunc: elasticQuota.OnQuotaDelete,
	}, elasticQuota.ReplaceQuotas)
	if err != nil {
		return nil, err
	}

	nodeInformer := handle.SharedInformerFactory().Core().V1().Nodes().Informer()
	frameworkexthelper.ForceSyncFromInformer(ctx.Done(), handle.SharedInformerFactory(), nodeInformer, cache.ResourceEventHandlerFuncs{
		AddFunc:    elasticQuota.OnNodeAdd,
		UpdateFunc: elasticQuota.OnNodeUpdate,
		DeleteFunc: elasticQuota.OnNodeDelete,
	})

	podInformer := handle.SharedInformerFactory().Core().V1().Pods().Informer()
	frameworkexthelper.ForceSyncFromInformer(ctx.Done(), handle.SharedInformerFactory(), podInformer, cache.ResourceEventHandlerFuncs{
		AddFunc:    elasticQuota.OnPodAdd,
		UpdateFunc: elasticQuota.OnPodUpdate,
		DeleteFunc: elasticQuota.OnPodDelete,
	})

	if extendedHandle, ok := handle.(frameworkext.ExtendedHandle); ok {
		extendedHandle.RegisterForgetPodHandler(elasticQuota.handlePodDelete)
	}

	elasticQuota.migrateDefaultQuotaGroupsPod()

	return elasticQuota, nil
}

func (g *Plugin) Start() {
	go wait.Until(g.migrateDefaultQuotaGroupsPod, MigrateDefaultQuotaGroupsPodCycle, nil)
	klog.Infof("start migrate pod from defaultQuotaGroup")

	// Start background goroutine to periodically update quota parent snapshot
	if g.pluginArgs.EnableQueueHint {
		updateInterval := g.pluginArgs.QuotaSnapshotUpdateInterval.Duration
		go wait.Until(g.updateQuotaSnapshot, updateInterval, nil)
		klog.Infof("start background quota snapshot updater with interval %v", updateInterval)
	}
}

func (g *Plugin) NewControllers() ([]frameworkext.Controller, error) {
	quotaOverUsedRevokeController := NewQuotaOverUsedRevokeController(g)
	elasticQuotaController := NewElasticQuotaController(g)
	return []frameworkext.Controller{g, quotaOverUsedRevokeController, elasticQuotaController}, nil
}

func (g *Plugin) Name() string {
	return Name
}

func (g *Plugin) EventsToRegister() []framework.ClusterEventWithHint {
	// To register a custom event, follow the naming convention at:
	// https://git.k8s.io/kubernetes/pkg/scheduler/eventhandlers.go#L403-L410
	eqGVK := fmt.Sprintf("elasticquotas.v1alpha1.%v", scheduling.GroupName)
	events := []framework.ClusterEventWithHint{
		{Event: framework.ClusterEvent{Resource: framework.Pod, ActionType: framework.Delete}},
		{Event: framework.ClusterEvent{Resource: framework.GVK(eqGVK), ActionType: framework.All}},
	}

	// Only set QueueingHintFn if EnableQueueHint is enabled
	if g.pluginArgs.EnableQueueHint {
		events = []framework.ClusterEventWithHint{
			{Event: framework.ClusterEvent{Resource: framework.Pod, ActionType: framework.Delete}, QueueingHintFn: g.isSchedulableAfterPodDeletion},
			{Event: framework.ClusterEvent{Resource: framework.GVK(eqGVK), ActionType: framework.Update}, QueueingHintFn: g.isSchedulableAfterQuotaChanged},
		}
	}

	return events
}

func (g *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	quotaName, treeID := g.getPodAssociateQuotaNameAndTreeID(pod)
	if quotaName == "" {
		g.skipPostFilterState(cycleState)
		return nil, framework.NewStatus(framework.Skip)
	}

	mgr := g.GetGroupQuotaManagerForTree(treeID)
	if mgr == nil {
		return nil, framework.NewStatus(framework.Error, fmt.Sprintf("Could not find the specified ElasticQuotaManager for quota: %v, tree: %v", quotaName, treeID))
	}
	if g.pluginArgs.EnableRuntimeQuota {
		mgr.RefreshRuntime(quotaName)
	}
	quotaInfo := mgr.GetQuotaInfoByName(quotaName)
	if quotaInfo == nil {
		return nil, framework.NewStatus(framework.Error, fmt.Sprintf("Could not find the specified ElasticQuota"))
	}
	state := g.snapshotPostFilterState(quotaInfo, cycleState)

	podRequest := core.PodRequests(pod)
	podRequest = quotav1.Mask(podRequest, quotav1.ResourceNames(quotaInfo.CalculateInfo.Max))
	used := quotav1.Add(podRequest, state.used)
	if isLessEqual, exceedDimensions := quotav1.LessThanOrEqual(used, state.usedLimit); !isLessEqual {
		return nil, framework.NewStatus(framework.Unschedulable, fmt.Sprintf("Insufficient quotas, "+
			"quotaName: %v, runtime: %v, used: %v, pod's request: %v, exceedDimensions: %v",
			quotaName, printResourceList(state.usedLimit), printResourceList(state.used), printResourceList(podRequest), exceedDimensions))
	}

	if extension.IsPodNonPreemptible(pod) {
		quotaMin := state.quotaInfo.CalculateInfo.Min
		nonPreemptibleUsed := state.nonPreemptibleUsed
		addNonPreemptibleUsed := quotav1.Add(podRequest, nonPreemptibleUsed)
		if isLessEqual, exceedDimensions := quotav1.LessThanOrEqual(addNonPreemptibleUsed, quotaMin); !isLessEqual {
			return nil, framework.NewStatus(framework.Unschedulable, fmt.Sprintf("Insufficient non-preemptible quotas, "+
				"quotaName: %v, min: %v, nonPreemptibleUsed: %v, pod's request: %v, exceedDimensions: %v",
				quotaName, printResourceList(quotaMin), printResourceList(nonPreemptibleUsed), printResourceList(podRequest), exceedDimensions))
		}
	}

	for _, hookPlugin := range mgr.GetHookPlugins() {
		if err := hookPlugin.CheckPod(quotaName, pod); err != nil {
			return nil, framework.NewStatus(framework.Unschedulable,
				fmt.Sprintf("CheckPod failed for hook plugin %v, err: %v", hookPlugin.GetKey(), err))
		}
	}

	if g.pluginArgs.EnableCheckParentQuota {
		return nil, g.checkQuotaRecursive(mgr, quotaInfo.ParentName, []string{quotaInfo.ParentName, quotaName}, podRequest)
	}

	return nil, framework.NewStatus(framework.Success, "")
}

func (g *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return g
}

// getQuotaSnapshot gets quota snapshot for the given tree ID
// Returns the snapshot and whether it exists
func (g *Plugin) getQuotaSnapshot(treeID string) (*core.QuotaSnapshot, bool) {
	g.quotaSnapshotLock.RLock()
	defer g.quotaSnapshotLock.RUnlock()

	snapshot, exists := g.quotaSnapshot[treeID]
	return snapshot, exists
}

// getQuotaToTreeMapCopy creates a copy of quotaToTreeMap
// This is thread-safe and returns a new map that can be safely used without locking
func (g *Plugin) getQuotaToTreeMapCopy() map[string]string {
	g.quotaToTreeMapLock.RLock()
	defer g.quotaToTreeMapLock.RUnlock()

	quotaToTreeMapCopy := make(map[string]string, len(g.quotaToTreeMap))
	for k, v := range g.quotaToTreeMap {
		quotaToTreeMapCopy[k] = v
	}
	return quotaToTreeMapCopy
}

// getPodAssociateQuotaNameAndTreeIDFromSnapshot gets quota name and tree ID using snapshot
// This avoids locking quotaToTreeMap
func (g *Plugin) getPodAssociateQuotaNameAndTreeIDFromSnapshot(pod *corev1.Pod) (string, string) {
	quotaName := g.GetQuotaName(pod)
	if quotaName == "" {
		return "", ""
	}

	g.quotaToTreeMapSnapshotLock.RLock()
	treeID, ok := g.quotaToTreeMapSnapshot[quotaName]
	g.quotaToTreeMapSnapshotLock.RUnlock()

	if ok {
		return quotaName, treeID
	}

	// If not found in snapshot, fallback to default quota
	if k8sfeature.DefaultFeatureGate.Enabled(features.DisableDefaultQuota) {
		return "", ""
	}
	return extension.DefaultQuotaName, ""
}

// isSchedulableAfterQuotaChanged determines if a pod becomes schedulable after quota is updated.
// QueueAfterBackoff is default queueingHintFn behavior.
func (g *Plugin) isSchedulableAfterQuotaChanged(logger klog.Logger, pod *corev1.Pod, oldObj, newObj interface{}) framework.QueueingHint {
	originalQuota, modifiedQuota, err := schedutil.As[*apiv1alpha1.ElasticQuota](oldObj, newObj)
	if err != nil {
		logger.Error(err, "Failed to convert oldObj or newObj to ElasticQuota", "oldObj", oldObj, "newObj", newObj)
		return framework.QueueAfterBackoff
	}

	if originalQuota == nil || modifiedQuota == nil {
		return framework.QueueAfterBackoff
	}

	// Use snapshot to get pod quota name and tree ID without locking
	podQuotaName, podTreeID := g.getPodAssociateQuotaNameAndTreeIDFromSnapshot(pod)
	if podQuotaName == "" {
		return framework.QueueSkip
	}

	// Check if modified quota is in the same tree as the pod
	modifiedQuotaTreeID := extension.GetQuotaTreeID(modifiedQuota)
	if modifiedQuotaTreeID != podTreeID {
		return framework.QueueSkip
	}

	mgr := g.GetGroupQuotaManagerForTree(podTreeID)
	if mgr == nil {
		return framework.QueueSkip
	}

	// Create quota info from original and modified quota
	oldQuotaInfo := core.NewQuotaInfoFromQuota(originalQuota)
	newQuotaInfo := core.NewQuotaInfoFromQuota(modifiedQuota)

	hasChanged := oldQuotaInfo.IsQuotaChange(newQuotaInfo) || mgr.IsQuotaUpdated(oldQuotaInfo, newQuotaInfo, modifiedQuota)
	if !hasChanged {
		return framework.QueueSkip
	}

	// Quota has changed, check if modified quota is in the pod's path to root
	if modifiedQuota.Name == podQuotaName {
		// Modified quota is the pod's quota, allow queueing
		return framework.QueueAfterBackoff
	}

	// Modified quota is not the pod's quota, check if it's in the path to root
	if g.pluginArgs.EnableCheckParentQuota {
		snapshot, exists := g.getQuotaSnapshot(podTreeID)

		if !exists || snapshot == nil {
			return framework.QueueAfterBackoff
		}

		// Get the path from pod's quota to root using the snapshot
		parentPath := snapshot.GetQuotaPathToRoot(podQuotaName)
		for _, quotaNameInPath := range parentPath {
			if quotaNameInPath == modifiedQuota.Name {
				return framework.QueueAfterBackoff
			}
		}
	}

	// Modified quota is not in the pod's path to root, skip
	return framework.QueueSkip
}

// isSchedulableAfterPodDeletion determines if a pod becomes schedulable after another pod is deleted.
// QueueAfterBackoff is default queueingHintFn behavior.
func (g *Plugin) isSchedulableAfterPodDeletion(logger klog.Logger, pod *corev1.Pod, oldObj, newObj interface{}) framework.QueueingHint {
	deletedPod, _, err := schedutil.As[*corev1.Pod](oldObj, newObj)
	if err != nil {
		logger.Error(err, "Failed to convert oldObj to Pod in isSchedulableAfterPodDeletion", "oldObj", oldObj, "newObj", newObj)
		return framework.QueueAfterBackoff
	}

	if deletedPod == nil {
		return framework.QueueAfterBackoff
	}

	// Use snapshot to get quota names and tree IDs without locking
	deletedPodQuotaName, deletedPodTreeID := g.getPodAssociateQuotaNameAndTreeIDFromSnapshot(deletedPod)
	if deletedPodQuotaName == "" {
		return framework.QueueSkip
	}

	podQuotaName, podTreeID := g.getPodAssociateQuotaNameAndTreeIDFromSnapshot(pod)
	if podQuotaName == "" {
		return framework.QueueSkip
	}

	// Check if deleted pod and unschedulable pod are in the same tree
	if deletedPodTreeID != podTreeID {
		return framework.QueueSkip
	}

	snapshot, exists := g.getQuotaSnapshot(podTreeID)
	if !exists || snapshot == nil {
		return framework.QueueAfterBackoff
	}

	// Get quota info from snapshot and check if pod is assigned
	quotaInfo := snapshot.GetQuotaInfoByName(deletedPodQuotaName)
	if quotaInfo != nil && !quotaInfo.CheckPodIsAssigned(deletedPod) {
		return framework.QueueSkip
	}

	if deletedPodQuotaName == podQuotaName {
		// Deleted pod is in the same quota as the unschedulable pod, allow queueing
		return framework.QueueAfterBackoff
	}

	// Check if deleted pod's quota is in the unschedulable pod's path to root
	if g.pluginArgs.EnableCheckParentQuota {
		// Get the path from unschedulable pod's quota to root using the snapshot
		parentPath := snapshot.GetQuotaPathToRoot(podQuotaName)
		for _, quotaNameInPath := range parentPath {
			if quotaNameInPath == deletedPodQuotaName {
				// Deleted pod's quota is in the path to root, allow queueing
				return framework.QueueAfterBackoff
			}
		}
	}

	// Deleted pod's quota is not in the unschedulable pod's path to root, skip
	return framework.QueueSkip
}

// AddPod is called by the framework while trying to evaluate the impact
// of adding podToAdd to the node while scheduling podToSchedule.
func (g *Plugin) AddPod(ctx context.Context, state *framework.CycleState, podToSchedule *corev1.Pod, podInfoToAdd *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	postFilterState, err := getPostFilterState(state)
	if err != nil {
		klog.ErrorS(err, "Failed to read postFilterState from cycleState", "elasticQuotaSnapshotKey", postFilterState)
		return framework.NewStatus(framework.Error, err.Error())
	}

	if postFilterState.skip {
		return framework.NewStatus(framework.Success, "")
	}

	if postFilterState.quotaInfo.IsPodExist(podInfoToAdd.Pod) {
		podReq := core.PodRequests(podInfoToAdd.Pod)
		podReq = quotav1.Mask(podReq, quotav1.ResourceNames(postFilterState.quotaInfo.CalculateInfo.Max))
		postFilterState.used = quotav1.Add(postFilterState.used, podReq)
	}
	return framework.NewStatus(framework.Success, "")
}

// RemovePod is called by the framework while trying to evaluate the impact
// of removing podToRemove from the node while scheduling podToSchedule.
func (g *Plugin) RemovePod(ctx context.Context, state *framework.CycleState, podToSchedule *corev1.Pod, podInfoToRemove *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	postFilterState, err := getPostFilterState(state)
	if err != nil {
		klog.ErrorS(err, "Failed to read postFilterState from cycleState", "elasticQuotaSnapshotKey", postFilterState)
		return framework.NewStatus(framework.Error, err.Error())
	}

	if postFilterState.skip {
		return framework.NewStatus(framework.Success, "")
	}

	if postFilterState.quotaInfo.IsPodExist(podInfoToRemove.Pod) {
		podReq := core.PodRequests(podInfoToRemove.Pod)
		podReq = quotav1.Mask(podReq, quotav1.ResourceNames(postFilterState.quotaInfo.CalculateInfo.Max))
		postFilterState.used = quotav1.SubtractWithNonNegativeResult(postFilterState.used, podReq)
	}
	return framework.NewStatus(framework.Success, "")
}

// PostFilter modify the defaultPreemption, only allow pods in the same quota can preempt others.
func (g *Plugin) PostFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, filteredNodeStatusMap framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	defer func() {
		metrics.PreemptionAttempts.Inc()
	}()

	pe := preemption.Evaluator{
		PluginName: Name,
		Handler:    g.handle,
		PodLister:  g.podLister,
		PdbLister:  g.pdbLister,
		State:      state,
		Interface:  g,
	}

	result, status := pe.Preempt(ctx, pod, filteredNodeStatusMap)
	if status.Message() != "" {
		return result, framework.NewStatus(status.Code(), "preemption: "+status.Message())
	}
	return result, status
}

func (g *Plugin) Reserve(ctx context.Context, state *framework.CycleState, p *corev1.Pod, nodeName string) *framework.Status {
	quotaName, treeID := g.getPodAssociateQuotaNameAndTreeID(p)
	if quotaName == "" {
		return framework.NewStatus(framework.Success, "")
	}

	mgr := g.GetGroupQuotaManagerForTree(treeID)
	if mgr == nil {
		klog.Errorf("failed reserve pod %v/%v, quota manager not found, quota: %v, tree: %v", p.Namespace, p.Name, quotaName, treeID)
		return framework.NewStatus(framework.Error, fmt.Sprintf("quota manager not found, quota: %v, tree: %v", quotaName, treeID))
	}

	mgr.ReservePod(quotaName, p)
	return framework.NewStatus(framework.Success, "")
}

func (g *Plugin) Unreserve(ctx context.Context, state *framework.CycleState, p *corev1.Pod, nodeName string) {
	quotaName, treeID := g.getPodAssociateQuotaNameAndTreeID(p)
	if quotaName == "" {
		return
	}

	mgr := g.GetGroupQuotaManagerForTree(treeID)
	if mgr == nil {
		klog.Errorf("failed unreserve pod %v/%v, quota manager not found, quota: %v, tree: %s", p.Namespace, p.Name, quotaName, treeID)
		return
	}
	mgr.UnreservePod(quotaName, p)
}

func (g *Plugin) GetQuotaInformer() cache.SharedIndexInformer { // expose for extensions
	return g.quotaInformer
}

// updateQuotaSnapshot periodically updates quota snapshot for all quota trees
// This runs in background and doesn't block the main scheduling path
func (g *Plugin) updateQuotaSnapshot() {
	// Copy quotaToTreeMap
	quotaToTreeMapCopy := g.getQuotaToTreeMapCopy()

	// Get managers and generate snapshots
	newSnapshots := make(map[string]*core.QuotaSnapshot)
	g.quotaManagerLock.RLock()
	for treeID, mgr := range g.groupQuotaManagersForQuotaTree {
		if mgr != nil {
			if snapshot := mgr.GetQuotaSnapshot(); snapshot != nil {
				newSnapshots[treeID] = snapshot
			}
		}
	}
	if g.groupQuotaManager != nil {
		if snapshot := g.groupQuotaManager.GetQuotaSnapshot(); snapshot != nil {
			newSnapshots[""] = snapshot
		}
	}
	g.quotaManagerLock.RUnlock()

	// Update snapshots
	g.quotaToTreeMapSnapshotLock.Lock()
	g.quotaToTreeMapSnapshot = quotaToTreeMapCopy
	g.quotaToTreeMapSnapshotLock.Unlock()

	g.quotaSnapshotLock.Lock()
	g.quotaSnapshot = newSnapshots
	g.quotaSnapshotLock.Unlock()
}
