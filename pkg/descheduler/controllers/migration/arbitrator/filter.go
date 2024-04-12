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

package arbitrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"
	kubecontroller "k8s.io/kubernetes/pkg/controller"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8sdeschedulerapi "sigs.k8s.io/descheduler/pkg/api"

	sev1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/controllerfinder"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/util"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/options"
	evictionsutil "github.com/koordinator-sh/koordinator/pkg/descheduler/evictions"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/fieldindex"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework/plugins/kubernetes/defaultevictor"
	podutil "github.com/koordinator-sh/koordinator/pkg/descheduler/pod"
	pkgutil "github.com/koordinator-sh/koordinator/pkg/util"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

type filter struct {
	client client.Client
	clock  clock.RealClock

	nonRetryablePodFilter framework.FilterFunc
	retryablePodFilter    framework.FilterFunc
	defaultFilterPlugin   framework.FilterPlugin

	args             *deschedulerconfig.MigrationControllerArgs
	controllerFinder controllerfinder.Interface
	objectLimiters   map[types.UID]*rate.Limiter
	limiterCache     *gocache.Cache
	limiterLock      sync.Mutex

	arbitratedPodMigrationJobs map[types.UID]bool
	arbitratedMapLock          sync.Mutex
}

func newFilter(args *deschedulerconfig.MigrationControllerArgs, handle framework.Handle) (*filter, error) {
	controllerFinder, err := controllerfinder.New(options.Manager)
	if err != nil {
		return nil, err
	}
	f := &filter{
		client:                     options.Manager.GetClient(),
		args:                       args,
		controllerFinder:           controllerFinder,
		clock:                      clock.RealClock{},
		arbitratedPodMigrationJobs: map[types.UID]bool{},
	}
	if err := f.initFilters(args, handle); err != nil {
		return nil, err
	}
	f.initObjectLimiters()
	return f, nil
}

func (f *filter) initFilters(args *deschedulerconfig.MigrationControllerArgs, handle framework.Handle) error {
	defaultEvictorArgs := &defaultevictor.DefaultEvictorArgs{
		NodeFit:                 args.NodeFit,
		NodeSelector:            args.NodeSelector,
		EvictLocalStoragePods:   args.EvictLocalStoragePods,
		EvictSystemCriticalPods: args.EvictSystemCriticalPods,
		IgnorePvcPods:           args.IgnorePvcPods,
		EvictFailedBarePods:     args.EvictFailedBarePods,
		LabelSelector:           args.LabelSelector,
	}
	if args.PriorityThreshold != nil {
		defaultEvictorArgs.PriorityThreshold = &k8sdeschedulerapi.PriorityThreshold{
			Name:  args.PriorityThreshold.Name,
			Value: args.PriorityThreshold.Value,
		}
	}
	defaultEvictor, err := defaultevictor.New(defaultEvictorArgs, handle)
	if err != nil {
		return err
	}

	var includedNamespaces, excludedNamespaces sets.String
	if args.Namespaces != nil {
		includedNamespaces = sets.NewString(args.Namespaces.Include...)
		excludedNamespaces = sets.NewString(args.Namespaces.Exclude...)
	}

	filterPlugin := defaultEvictor.(framework.FilterPlugin)
	wrapFilterFuncs := podutil.WrapFilterFuncs(
		util.FilterPodWithMaxEvictionCost,
		filterPlugin.Filter,
		f.filterExpectedReplicas,
	)
	podFilter, err := podutil.NewOptions().
		WithFilter(wrapFilterFuncs).
		WithNamespaces(includedNamespaces).
		WithoutNamespaces(excludedNamespaces).
		BuildFilterFunc()
	if err != nil {
		return err
	}
	retriablePodFilters := podutil.WrapFilterFuncs(
		f.filterLimitedObject,
		f.filterMaxMigratingPerNode,
		f.filterMaxMigratingPerNamespace,
		f.filterMaxMigratingOrUnavailablePerWorkload,
	)
	f.retryablePodFilter = func(pod *corev1.Pod) bool {
		return evictionsutil.HaveEvictAnnotation(pod) || retriablePodFilters(pod)
	}
	f.nonRetryablePodFilter = podFilter
	f.defaultFilterPlugin = defaultEvictor.(framework.FilterPlugin)
	return nil
}

func (f *filter) reservationFilter(pod *corev1.Pod) bool {
	if sev1alpha1.PodMigrationJobMode(f.args.DefaultJobMode) != sev1alpha1.PodMigrationJobModeReservationFirst {
		return true
	}

	if pkgutil.IsIn(f.args.SchedulerNames, pod.Spec.SchedulerName) {
		return true
	}

	klog.Errorf("Pod %q can not be migrated by ReservationFirst mode because pod.schedulerName=%s but scheduler of pmj controller assigned is %s", klog.KObj(pod), pod.Spec.SchedulerName, f.args.SchedulerNames)
	return false
}

func (f *filter) forEachAvailableMigrationJobs(listOpts *client.ListOptions, handler func(job *sev1alpha1.PodMigrationJob) bool, expectedPhaseContexts ...phaseContext) {
	jobList := &sev1alpha1.PodMigrationJobList{}
	err := f.client.List(context.TODO(), jobList, listOpts, utilclient.DisableDeepCopy)
	if err != nil {
		klog.Errorf("failed to get PodMigrationJobList, err: %v", err)
		return
	}

	if len(expectedPhaseContexts) == 0 {
		expectedPhaseContexts = []phaseContext{
			{phase: sev1alpha1.PodMigrationJobRunning, checkArbitration: false},
			{phase: sev1alpha1.PodMigrationJobPending, checkArbitration: false},
		}
	}

	for i := range jobList.Items {
		job := &jobList.Items[i]
		phase := job.Status.Phase
		if phase == "" {
			phase = sev1alpha1.PodMigrationJobPending
		}
		found := false
		for _, v := range expectedPhaseContexts {
			if phase == v.phase && (!v.checkArbitration || f.checkJobPassedArbitration(job.UID)) {
				found = true
				break
			}
		}
		if found && !handler(job) {
			break
		}
	}
}

func (f *filter) filterExistingPodMigrationJob(pod *corev1.Pod) bool {
	return !f.existingPodMigrationJob(pod)
}

func (f *filter) existingPodMigrationJob(pod *corev1.Pod, expectedPhaseContexts ...phaseContext) bool {
	opts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexJobByPodUID, string(pod.UID))}
	existing := false
	f.forEachAvailableMigrationJobs(opts, func(job *sev1alpha1.PodMigrationJob) bool {
		if podRef := job.Spec.PodRef; podRef != nil && podRef.UID == pod.UID {
			existing = true
		}
		return !existing
	}, expectedPhaseContexts...)

	if !existing {
		opts = &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexJobPodNamespacedName, fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))}
		f.forEachAvailableMigrationJobs(opts, func(job *sev1alpha1.PodMigrationJob) bool {
			if podRef := job.Spec.PodRef; podRef != nil && podRef.Namespace == pod.Namespace && podRef.Name == pod.Name {
				existing = true
			}
			return !existing
		}, expectedPhaseContexts...)
	}
	return existing
}

func (f *filter) filterMaxMigratingPerNode(pod *corev1.Pod) bool {
	if pod.Spec.NodeName == "" || f.args.MaxMigratingPerNode == nil || *f.args.MaxMigratingPerNode <= 0 {
		return true
	}

	podList := &corev1.PodList{}
	listOpts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexPodByNodeName, pod.Spec.NodeName)}
	err := f.client.List(context.TODO(), podList, listOpts, utilclient.DisableDeepCopy)
	if err != nil {
		return true
	}
	if len(podList.Items) == 0 {
		return true
	}

	var expectedPhaseContexts []phaseContext
	if checkPodArbitrating(pod) {
		expectedPhaseContexts = []phaseContext{
			{phase: sev1alpha1.PodMigrationJobRunning, checkArbitration: false},
			{phase: sev1alpha1.PodMigrationJobPending, checkArbitration: true},
		}
	}

	count := 0
	for i := range podList.Items {
		v := &podList.Items[i]
		if v.UID != pod.UID &&
			v.Spec.NodeName == pod.Spec.NodeName &&
			f.existingPodMigrationJob(v, expectedPhaseContexts...) {
			count++
		}
	}

	maxMigratingPerNode := int(*f.args.MaxMigratingPerNode)
	exceeded := count >= maxMigratingPerNode
	if exceeded {
		klog.V(4).InfoS("Pod fails the following checks", "pod", klog.KObj(pod),
			"checks", "maxMigratingPerNode", "node", pod.Spec.NodeName, "count", count, "maxMigratingPerNode", maxMigratingPerNode)
	}
	return !exceeded
}

func (f *filter) filterMaxMigratingPerNamespace(pod *corev1.Pod) bool {
	if f.args.MaxMigratingPerNamespace == nil || *f.args.MaxMigratingPerNamespace <= 0 {
		return true
	}

	var expectedPhaseContexts []phaseContext
	if checkPodArbitrating(pod) {
		expectedPhaseContexts = []phaseContext{
			{phase: sev1alpha1.PodMigrationJobRunning, checkArbitration: false},
			{phase: sev1alpha1.PodMigrationJobPending, checkArbitration: true},
		}
	}

	opts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexJobByPodNamespace, pod.Namespace)}
	count := 0
	f.forEachAvailableMigrationJobs(opts, func(job *sev1alpha1.PodMigrationJob) bool {
		if podRef := job.Spec.PodRef; podRef != nil && podRef.UID != pod.UID && podRef.Namespace == pod.Namespace {
			count++
		}
		return true
	}, expectedPhaseContexts...)

	maxMigratingPerNamespace := int(*f.args.MaxMigratingPerNamespace)
	exceeded := count >= maxMigratingPerNamespace
	if exceeded {
		klog.V(4).InfoS("Pod fails the following checks", "pod", klog.KObj(pod),
			"checks", "maxMigratingPerNamespace", "namespace", pod.Namespace, "count", count, "maxMigratingPerNamespace", maxMigratingPerNamespace)
	}
	return !exceeded
}

func (f *filter) filterMaxMigratingOrUnavailablePerWorkload(pod *corev1.Pod) bool {
	ownerRef := metav1.GetControllerOf(pod)
	if ownerRef == nil {
		return true
	}
	pods, expectedReplicas, err := f.controllerFinder.GetPodsForRef(ownerRef, pod.Namespace, nil, false)
	if err != nil {
		return false
	}

	maxMigrating, err := util.GetMaxMigrating(int(expectedReplicas), f.args.MaxMigratingPerWorkload)
	if err != nil {
		return false
	}
	maxUnavailable, err := util.GetMaxUnavailable(int(expectedReplicas), f.args.MaxUnavailablePerWorkload)
	if err != nil {
		return false
	}

	var expectedPhaseContext []phaseContext
	if checkPodArbitrating(pod) {
		expectedPhaseContext = []phaseContext{
			{phase: sev1alpha1.PodMigrationJobRunning, checkArbitration: false},
			{phase: sev1alpha1.PodMigrationJobPending, checkArbitration: true},
		}
	}
	opts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexJobByPodNamespace, pod.Namespace)}
	migratingPods := map[types.NamespacedName]struct{}{}
	f.forEachAvailableMigrationJobs(opts, func(job *sev1alpha1.PodMigrationJob) bool {
		podRef := job.Spec.PodRef
		if podRef == nil || podRef.UID == pod.UID {
			return true
		}

		podNamespacedName := types.NamespacedName{
			Namespace: podRef.Namespace,
			Name:      podRef.Name,
		}
		p := &corev1.Pod{}
		err := f.client.Get(context.TODO(), podNamespacedName, p)
		if err != nil {
			klog.Errorf("Failed to get Pod %q, err: %v", podNamespacedName, err)
		} else {
			innerPodOwnerRef := metav1.GetControllerOf(p)
			if innerPodOwnerRef != nil && innerPodOwnerRef.UID == ownerRef.UID {
				migratingPods[podNamespacedName] = struct{}{}
			}
		}
		return true
	}, expectedPhaseContext...)

	if len(migratingPods) > 0 {
		exceeded := len(migratingPods) >= maxMigrating
		if exceeded {
			klog.V(4).InfoS("Pod fails the following checks", "pod", klog.KObj(pod),
				"checks", "maxMigratingPerWorkload", "owner", fmt.Sprintf("%s/%s/%s(%s)", ownerRef.Name, ownerRef.Kind, ownerRef.APIVersion, ownerRef.UID),
				"migratingPods", len(migratingPods), "maxMigratingPerWorkload", maxMigrating)
			return false
		}
	}

	unavailablePods := f.getUnavailablePods(pods)
	mergeUnavailableAndMigratingPods(unavailablePods, migratingPods)
	exceeded := len(unavailablePods) >= maxUnavailable
	if exceeded {
		klog.V(4).Infof("The workload %s/%s/%s(%s) of Pod %q has %d unavailable Pods that exceed MaxUnavailablePerWorkload %d",
			ownerRef.Name, ownerRef.Kind, ownerRef.APIVersion, ownerRef.UID, klog.KObj(pod), len(unavailablePods), maxUnavailable)
		return false
	}
	return true
}

func (f *filter) filterExpectedReplicas(pod *corev1.Pod) bool {
	ownerRef := metav1.GetControllerOf(pod)
	if ownerRef == nil {
		return true
	}
	_, expectedReplicas, err := f.controllerFinder.GetPodsForRef(ownerRef, pod.Namespace, nil, false)
	if err != nil {
		klog.Errorf("filterExpectedReplicas, getPodsForRef err: %s", err.Error())
		return false
	}

	maxMigrating, err := util.GetMaxMigrating(int(expectedReplicas), f.args.MaxMigratingPerWorkload)
	if err != nil {
		klog.Errorf("filterExpectedReplicas, getMaxMigrating err: %s", err.Error())
		return false
	}
	maxUnavailable, err := util.GetMaxUnavailable(int(expectedReplicas), f.args.MaxUnavailablePerWorkload)
	if err != nil {
		klog.Errorf("filterExpectedReplicas, getMaxUnavailable err: %s", err.Error())
		return false
	}
	if f.args.SkipCheckExpectedReplicas == nil || !*f.args.SkipCheckExpectedReplicas {
		// TODO(joseph): There are f few special scenarios where should we allow eviction?
		if expectedReplicas == 1 || int(expectedReplicas) == maxMigrating || int(expectedReplicas) == maxUnavailable {
			klog.V(4).InfoS("Pod fails the following checks", "pod", klog.KObj(pod), "checks", "expectedReplicas",
				"owner", fmt.Sprintf("%s/%s/%s(%s)", ownerRef.Name, ownerRef.Kind, ownerRef.APIVersion, ownerRef.UID),
				"maxMigrating", maxMigrating, "maxUnavailable", maxUnavailable, "expectedReplicas", expectedReplicas)
			return false
		}
	}
	return true
}

func (f *filter) getUnavailablePods(pods []*corev1.Pod) map[types.NamespacedName]struct{} {
	unavailablePods := make(map[types.NamespacedName]struct{})
	for _, pod := range pods {
		if kubecontroller.IsPodActive(pod) && k8spodutil.IsPodReady(pod) {
			continue
		}
		k := types.NamespacedName{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		}
		unavailablePods[k] = struct{}{}
	}
	return unavailablePods
}

func mergeUnavailableAndMigratingPods(unavailablePods, migratingPods map[types.NamespacedName]struct{}) {
	for k, v := range migratingPods {
		unavailablePods[k] = v
	}
}

func (f *filter) trackEvictedPod(pod *corev1.Pod) {
	if f.objectLimiters == nil || f.limiterCache == nil {
		return
	}
	ownerRef := metav1.GetControllerOf(pod)
	if ownerRef == nil {
		return
	}

	objectLimiterArgs, ok := f.args.ObjectLimiters[deschedulerconfig.MigrationLimitObjectWorkload]
	if !ok || objectLimiterArgs.Duration.Seconds() == 0 {
		return
	}

	var maxMigratingReplicas int
	if expectedReplicas, err := f.controllerFinder.GetExpectedScaleForPod(pod); err == nil {
		maxMigrating := objectLimiterArgs.MaxMigrating
		if maxMigrating == nil {
			maxMigrating = f.args.MaxMigratingPerWorkload
		}
		maxMigratingReplicas, _ = util.GetMaxMigrating(int(expectedReplicas), maxMigrating)
	}
	if maxMigratingReplicas == 0 {
		return
	}

	f.limiterLock.Lock()
	defer f.limiterLock.Unlock()

	uid := ownerRef.UID
	limit := rate.Limit(maxMigratingReplicas) / rate.Limit(objectLimiterArgs.Duration.Seconds())
	limiter := f.objectLimiters[uid]
	if limiter == nil {
		limiter = rate.NewLimiter(limit, 1)
		f.objectLimiters[uid] = limiter
	} else if limiter.Limit() != limit {
		limiter.SetLimit(limit)
	}

	if !limiter.AllowN(f.clock.Now(), 1) {
		klog.Infof("The workload %s/%s/%s has been frequently descheduled recently and needs to be limited for f period of time", ownerRef.Name, ownerRef.Kind, ownerRef.APIVersion)
	}
	f.limiterCache.Set(string(uid), 0, gocache.DefaultExpiration)
}

func (f *filter) filterLimitedObject(pod *corev1.Pod) bool {
	if f.objectLimiters == nil || f.limiterCache == nil {
		return true
	}
	objectLimiterArgs, ok := f.args.ObjectLimiters[deschedulerconfig.MigrationLimitObjectWorkload]
	if !ok || objectLimiterArgs.Duration.Duration == 0 {
		return true
	}
	if ownerRef := metav1.GetControllerOf(pod); ownerRef != nil {
		f.limiterLock.Lock()
		defer f.limiterLock.Unlock()
		if limiter := f.objectLimiters[ownerRef.UID]; limiter != nil {
			if remainTokens := limiter.Tokens() - float64(1); remainTokens < 0 {
				klog.V(4).InfoS("Pod fails the following checks", "pod", klog.KObj(pod), "checks", "limitedObject",
					"owner", fmt.Sprintf("%s/%s/%s", ownerRef.Name, ownerRef.Kind, ownerRef.APIVersion))
				return false
			}
		}
	}
	return true
}

func (f *filter) initObjectLimiters() {
	var trackExpiration time.Duration
	for _, v := range f.args.ObjectLimiters {
		if v.Duration.Duration > trackExpiration {
			trackExpiration = v.Duration.Duration
		}
	}
	if trackExpiration > 0 {
		f.objectLimiters = make(map[types.UID]*rate.Limiter)
		limiterExpiration := trackExpiration + trackExpiration/2
		f.limiterCache = gocache.New(limiterExpiration, limiterExpiration)
		f.limiterCache.OnEvicted(func(s string, _ interface{}) {
			f.limiterLock.Lock()
			defer f.limiterLock.Unlock()
			delete(f.objectLimiters, types.UID(s))
		})
	}
}

func (f *filter) checkJobPassedArbitration(uid types.UID) bool {
	f.arbitratedMapLock.Lock()
	defer f.arbitratedMapLock.Unlock()
	return f.arbitratedPodMigrationJobs[uid]
}

func (f *filter) markJobPassedArbitration(uid types.UID) {
	f.arbitratedMapLock.Lock()
	defer f.arbitratedMapLock.Unlock()
	f.arbitratedPodMigrationJobs[uid] = true
}

func (f *filter) removeJobPassedArbitration(uid types.UID) {
	f.arbitratedMapLock.Lock()
	defer f.arbitratedMapLock.Unlock()
	delete(f.arbitratedPodMigrationJobs, uid)
}

type phaseContext struct {
	phase            sev1alpha1.PodMigrationJobPhase
	checkArbitration bool
}
