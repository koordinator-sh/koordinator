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

package quotaevaluate

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/util/workqueue"
	apiresource "k8s.io/component-helpers/resource"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

// podResources are the set of resources managed by quota associated with pods.
var podResources = []corev1.ResourceName{
	corev1.ResourceCPU,
	corev1.ResourceMemory,
	corev1.ResourceEphemeralStorage,
	corev1.ResourceRequestsCPU,
	corev1.ResourceRequestsMemory,
	corev1.ResourceRequestsEphemeralStorage,

	// batch resource
	extension.BatchCPU,
	extension.BatchMemory,

	// mid resource
	extension.MidCPU,
	extension.MidMemory,

	// gpu resource
	extension.ResourceGPU,
	extension.ResourceNvidiaGPU,
	extension.ResourceGPUShared,
	extension.ResourceGPUMemoryRatio,
}

// podResourcesSet is a pre-computed set of podResources for O(1) lookup.
var podResourcesSet = sets.New(podResources...)

const (
	// quotaUpdateMinRetries is the fixed lower bound of UpdateQuotaStatus retries
	// per batch, matching the original hardcoded value so small batches keep the
	// previous behavior exactly.
	quotaUpdateMinRetries = 3
)

// QuotaUpdateMaxRetries is the upper bound of UpdateQuotaStatus retries per batch.
// Default 3 matches pre-optimization behavior. Recommended 10 for high-contention
// multi-replica deployments to allow dynamic retry scaling with batch size
// (retries grow by 1 per doubling of batch size, capped at this value).
var QuotaUpdateMaxRetries = quotaUpdateMinRetries

// QuotaUpdateRetryJitter is the per-retry decorrelation sleep upper bound for
// UpdateQuotaStatus conflicts. Actual sleep per retry is drawn from [jitter/2, jitter).
// Default 0 (disabled) matches pre-optimization behavior. Recommended 50ms for
// multi-replica webhook deployments to reduce conflict storms between peers.
var QuotaUpdateRetryJitter time.Duration

// QuotaUpdateMaxTotalJitter bounds cumulative jitter per batch so Evaluate()'s
// 10s evaluation timeout never gets consumed by jitter alone.
// Default 1s is a safe upper bound for most scenarios (10% of the 10s timeout).
// Jitter is only effective when --quota-update-retry-jitter > 0.
var QuotaUpdateMaxTotalJitter = 1 * time.Second

// QuotaUpdateConflictFreshGet controls whether UpdateQuotaStatus uses a non-cached
// (apiReader) read on conflict to refresh the local cache, so the next retry
// sees the latest ResourceVersion and avoids repeated conflicts.
// Default false matches pre-optimization behavior. Recommended true for
// environments where the cached client frequently returns stale ResourceVersions.
var QuotaUpdateConflictFreshGet bool

func InitFlags(fs *flag.FlagSet) {
	fs.IntVar(&QuotaUpdateMaxRetries, "quota-update-max-retries",
		QuotaUpdateMaxRetries,
		"Maximum number of UpdateQuotaStatus retries per batch (default 3). "+
			"Recommended 10 for high-contention multi-replica deployments "+
			"to allow dynamic retry scaling with batch size.")
	fs.DurationVar(&QuotaUpdateRetryJitter, "quota-update-retry-jitter",
		QuotaUpdateRetryJitter,
		"Per-retry decorrelation sleep upper bound for UpdateQuotaStatus conflicts (default 0, disabled). "+
			"Recommended 50ms for multi-replica webhook deployments to reduce conflict storms. "+
			"Actual sleep is in [jitter/2, jitter).")
	fs.DurationVar(&QuotaUpdateMaxTotalJitter, "quota-update-max-total-jitter",
		QuotaUpdateMaxTotalJitter,
		"Per-batch cumulative jitter cap for UpdateQuotaStatus retries (default 1s). "+
			"Bounds worst-case jitter overhead within Evaluate()'s 10s evaluation timeout. "+
			"Only effective when --quota-update-retry-jitter > 0.")
	fs.BoolVar(&QuotaUpdateConflictFreshGet, "quota-update-conflict-fresh-get",
		QuotaUpdateConflictFreshGet,
		"Use non-cached apiReader on update conflict to refresh local cache (default false). "+
			"Recommended true to reduce repeat conflicts caused by stale cached ResourceVersions.")
}

// retriesForBatch returns the retry budget for a batch in which n pods have
// contributed a non-zero delta. Equivalent to the legacy hardcoded value when
// n <= 1, and grows by 1 with every doubling of n, capped at QuotaUpdateMaxRetries.
//
// Why: expected failed admissions per batch is ~ n * p^k. Holding k constant lets
// that error term scale linearly with n. Bumping k by log2(n) keeps it roughly
// constant. Extra attempts only run on failure (with probability p^k), so the
// marginal cost decays exponentially and steady-state throughput is unaffected.
func retriesForBatch(n int) int {
	maxK := QuotaUpdateMaxRetries
	if maxK < quotaUpdateMinRetries {
		maxK = quotaUpdateMinRetries
	}
	if n <= 1 {
		return quotaUpdateMinRetries
	}
	k := quotaUpdateMinRetries + int(math.Floor(math.Log2(float64(n))))
	if k > maxK {
		return maxK
	}
	return k
}

// jitterForNextUpdate decides how much jitter to grant the upcoming
// UpdateQuotaStatus call. Returns 0 when:
//   - jitter is non-positive (feature disabled), or
//   - no retry would happen after this update (remainingRetries <= 0), or
//   - granting more would exceed QuotaUpdateMaxTotalJitter for this batch.
func (e *quotaEvaluator) jitterForNextUpdate(ctx *quotaCheckContext, remainingRetries int) time.Duration {
	if QuotaUpdateRetryJitter <= 0 {
		return 0
	}
	if remainingRetries <= 0 {
		return 0
	}
	if ctx.totalJitterSpent+QuotaUpdateRetryJitter > QuotaUpdateMaxTotalJitter {
		return 0
	}
	return QuotaUpdateRetryJitter
}

// quotaCheckContext holds accumulated state across checkQuota invocations for performance
// optimization and retry fast-path support.
type quotaCheckContext struct {
	admission corev1.ResourceList
	fatal     error

	// used caches the most recently computed childRequest to avoid
	// repeated marshal/unmarshal round-trips within the batch loop.
	used corev1.ResourceList
	// delta is the sum of all successfully admitted Pod deltas in this batch.
	// Used for optimistic fast-path validation on retry.
	delta corev1.ResourceList
	names sets.Set[corev1.ResourceName]

	// totalJitterSpent accumulates per-batch jitter allocated to UpdateQuotaStatus
	// calls. Persists across recursive checkQuota calls; capped at
	// QuotaUpdateMaxTotalJitter so jitter cannot exhaust Evaluate()'s timeout.
	totalJitterSpent time.Duration
}

// maskResourceList removes entries from resources that are not in nameSet or have zero values.
func maskResourceList(resources corev1.ResourceList, nameSet sets.Set[corev1.ResourceName]) {
	for key, value := range resources {
		if !nameSet.Has(key) || value.IsZero() {
			delete(resources, key)
		}
	}
}

type Attributes struct {
	QuotaNamespace string
	QuotaName      string
	Operation      admissionv1.Operation
	Pod            *corev1.Pod
}

// Evaluator is used to see if quota constraints are satisfied.
type Evaluator interface {
	// Evaluate takes an operation and checks to see if quota constraints are satisfied.  It returns an error if they are not.
	// The default implementation processes related operations in chunks when possible.
	Evaluate(a *Attributes) error
}

type quotaEvaluator struct {
	quotaAccessor QuotaAccessor

	queue      *workqueue.Type
	workLock   sync.Mutex
	work       map[string][]*admissionWaiter
	dirtyWork  map[string][]*admissionWaiter
	inProgress sets.String

	workers int
	stopCh  <-chan struct{}
	init    sync.Once
}

type admissionWaiter struct {
	attributes *Attributes
	finished   chan struct{}
	result     error
}

type defaultDeny struct{}

func (defaultDeny) Error() string {
	return "DEFAULT DENY"
}

// IsDefaultDeny returns true if the error is defaultDeny
func IsDefaultDeny(err error) bool {
	if err == nil {
		return false
	}

	_, ok := err.(defaultDeny)
	return ok
}

func newAdmissionWaiter(a *Attributes) *admissionWaiter {
	return &admissionWaiter{
		attributes: a,
		finished:   make(chan struct{}),
		result:     defaultDeny{},
	}
}

func NewQuotaEvaluator(quotaAccessor QuotaAccessor, workers int, stopCh <-chan struct{}) Evaluator {
	evaluator := &quotaEvaluator{
		quotaAccessor: quotaAccessor,

		queue:      workqueue.NewNamed("admission_quota_controller"),
		work:       map[string][]*admissionWaiter{},
		dirtyWork:  map[string][]*admissionWaiter{},
		inProgress: sets.String{},

		workers: workers,
		stopCh:  stopCh,
	}

	return evaluator
}

// start begins watching and syncing.
func (e *quotaEvaluator) start() {
	defer utilruntime.HandleCrash()

	for i := 0; i < e.workers; i++ {
		go wait.Until(e.doWork, time.Second, e.stopCh)
	}
}

func (e *quotaEvaluator) shutdownOnStop() {
	<-e.stopCh
	klog.Infof("Shutting down quota evaluator")
	e.queue.ShutDown()
}

func (e *quotaEvaluator) doWork() {
	workFunc := func() bool {
		key, admissionAttributes, quit := e.getWork()
		if quit {
			return true
		}
		defer e.completeWork(key)
		if len(admissionAttributes) == 0 {
			return false
		}
		e.checkAttributes(key, admissionAttributes)
		return false
	}
	for {
		if quit := workFunc(); quit {
			klog.Infof("quota evaluator worker shutdown")
			return
		}
	}
}

func (e *quotaEvaluator) checkAttributes(key string, admissionAttributes []*admissionWaiter) {
	// notify all on exit
	defer func() {
		for _, admissionAttribute := range admissionAttributes {
			close(admissionAttribute.finished)
		}
	}()

	quota, err := e.quotaAccessor.GetQuota(key)
	if err != nil {
		for _, admissionAttribute := range admissionAttributes {
			admissionAttribute.result = err
		}
		return
	}

	startRetries := QuotaUpdateMaxRetries
	if startRetries < quotaUpdateMinRetries {
		startRetries = quotaUpdateMinRetries
	}
	e.checkQuota(quota, admissionAttributes, startRetries, nil)
}

func (e *quotaEvaluator) checkQuota(quota *v1alpha1.ElasticQuota, admissionAttributes []*admissionWaiter, remainingRetries int, ctx *quotaCheckContext) {
	ctx = e.initCtx(ctx, quota)

	fastPathOK := false
	// fast retry only happens when quota is changed in last round and resource names keep unchanged
	if ctx.fatal == nil && len(ctx.delta) > 0 {
		newUsage := quotav1.Add(ctx.used, ctx.delta)
		maskResourceList(newUsage, ctx.names)

		if allowed, _ := quotav1.LessThanOrEqual(newUsage, ctx.admission); !allowed {
			// cached delta is no longer admissible; drop it and rebuild via slow path
			ctx.delta = corev1.ResourceList{}
		} else {
			fastPathOK = true
			ctx.used = newUsage
		}
	}

	if !fastPathOK {
		changed := 0
		for i := range admissionAttributes {
			admissionAttribute := admissionAttributes[i]
			if !IsDefaultDeny(admissionAttribute.result) {
				continue
			}
			oldUsed := ctx.used.DeepCopy()
			err := e.checkRequest(quota, admissionAttribute.attributes, ctx)
			if err != nil {
				admissionAttribute.result = err
				continue
			}

			if !quotav1.Equals(oldUsed, ctx.used) {
				changed++
			} else {
				admissionAttribute.result = nil
			}
		}

		if changed <= 0 {
			return
		}
		// Narrow the retry budget to what this batch's real workload warrants.
		// remainingRetries is decremented on every recursion, so the min here
		// can only ratchet it further down -- a batch that shrinks across
		// retries gets a tighter budget but never a looser one.
		if retries := retriesForBatch(changed); retries < remainingRetries {
			remainingRetries = retries
		}
	}

	data, updateErr := json.Marshal(ctx.used)
	if updateErr == nil {
		newQuota := quota.DeepCopy()
		if newQuota.Annotations == nil {
			newQuota.Annotations = make(map[string]string)
		}
		newQuota.Annotations[extension.AnnotationChildRequest] = string(data)
		jitter := e.jitterForNextUpdate(ctx, remainingRetries)
		if jitter > 0 {
			ctx.totalJitterSpent += jitter
		}
		updateErr = e.quotaAccessor.UpdateQuotaStatus(newQuota, jitter)
	}

	if updateErr == nil {
		for _, admissionAttribute := range admissionAttributes {
			if IsDefaultDeny(admissionAttribute.result) {
				admissionAttribute.result = nil
			}
		}
		return
	}

	// at this point, errors are fatal.  Update all waiters without status to failed and return
	if remainingRetries <= 0 {
		for _, admissionAttribute := range admissionAttributes {
			if IsDefaultDeny(admissionAttribute.result) {
				admissionAttribute.result = updateErr
			}
		}
		return
	}

	newQuota, err := e.quotaAccessor.GetQuota(fmt.Sprintf("%s/%s", quota.Namespace, quota.Name))
	if err != nil {
		// this means that updates failed.  Anything with a default deny error has failed and we need to let them know
		for _, admissionAttribute := range admissionAttributes {
			if IsDefaultDeny(admissionAttribute.result) {
				admissionAttribute.result = updateErr
			}
		}
		return
	}

	// Pass ctx to enable fast-path in next recursion
	e.checkQuota(newQuota, admissionAttributes, remainingRetries-1, ctx)
}

func (e *quotaEvaluator) initCtx(ctx *quotaCheckContext, quota *v1alpha1.ElasticQuota) *quotaCheckContext {
	if ctx == nil {
		ctx = &quotaCheckContext{}
	}
	childRequest, childRequestErr := extension.GetChildRequest(quota)
	admission, admissionErr := GetQuotaAdmission(quota)
	ctx.admission = admission
	ctx.fatal = utilerrors.NewAggregate([]error{childRequestErr, admissionErr})
	if childRequest != nil {
		ctx.used = childRequest.DeepCopy()
	} else {
		ctx.used = corev1.ResourceList{}
	}
	names := sets.KeySet(quota.Spec.Max)
	// clean up when resource names changed
	if ctx.names.Len() == 0 || !names.Equal(ctx.names) {
		ctx.names, ctx.delta = names, corev1.ResourceList{}
	}
	return ctx
}

func (e *quotaEvaluator) Handles(a *Attributes) bool {
	if a.Operation == admissionv1.Create {
		return true
	}
	return false
}

func QuotaV1Pod(pod *corev1.Pod, clock clock.Clock) bool {
	if corev1.PodFailed == pod.Status.Phase || corev1.PodSucceeded == pod.Status.Phase {
		return false
	}
	if pod.DeletionTimestamp != nil && pod.DeletionGracePeriodSeconds != nil {
		now := clock.Now()
		deletionTime := pod.DeletionTimestamp.Time
		gracePeriod := time.Duration(*pod.DeletionGracePeriodSeconds) * time.Second
		if now.After(deletionTime.Add(gracePeriod)) {
			return false
		}
	}
	return true
}

func PodUsageFunc(pod *corev1.Pod, clock clock.Clock) (corev1.ResourceList, error) {
	if !QuotaV1Pod(pod, clock) {
		return corev1.ResourceList{}, nil
	}

	requests := apiresource.PodRequests(pod, apiresource.PodResourcesOptions{})

	return requests, nil
}

func (e *quotaEvaluator) checkRequest(quota *v1alpha1.ElasticQuota, a *Attributes, ctx *quotaCheckContext) error {
	if !e.Handles(a) {
		return nil
	}

	if ctx.fatal != nil {
		return ctx.fatal
	}

	if ctx.names.Intersection(podResourcesSet).Len() == 0 {
		return nil
	}

	requestedUsage, err := PodUsageFunc(a.Pod, clock.RealClock{})
	if err != nil {
		return err
	}

	// Filter requestedUsage to only include resources in quota.Spec.Max, removing zeros.
	maskResourceList(requestedUsage, ctx.names)
	if len(requestedUsage) == 0 {
		return nil
	}

	used, admission := ctx.used, ctx.admission

	// Use quotav1.Add (non-destructive) to preserve original 'used' for error messages
	newUsage := quotav1.Add(used, requestedUsage)
	maskedNewUsage := quotav1.Mask(newUsage, quotav1.ResourceNames(requestedUsage))

	if allowed, exceeded := quotav1.LessThanOrEqual(maskedNewUsage, admission); !allowed {
		failedRequestedUsage := quotav1.Mask(requestedUsage, exceeded)
		failedUsed := quotav1.Mask(used, exceeded)
		failedHard := quotav1.Mask(admission, exceeded)
		return fmt.Errorf("exceeded quota: %s/%s, requested: %s, used: %s, limited: %s",
			quota.Namespace, quota.Name,
			prettyPrint(failedRequestedUsage),
			prettyPrint(failedUsed),
			prettyPrint(failedHard))
	}

	// Accumulate delta and update cached usage for next iteration
	ctx.used = newUsage
	util.AddResourceList(ctx.delta, requestedUsage)

	return nil
}

func (e *quotaEvaluator) Evaluate(a *Attributes) error {
	e.init.Do(e.start)

	if !e.Handles(a) {
		return nil
	}
	waiter := newAdmissionWaiter(a)

	e.addWork(waiter)

	// wait for completion or timeout
	select {
	case <-waiter.finished:
	case <-time.After(10 * time.Second):
		return apierrors.NewInternalError(fmt.Errorf("elastic quota evaluation timed out"))
	}

	return waiter.result
}

func (e *quotaEvaluator) addWork(a *admissionWaiter) {
	e.workLock.Lock()
	defer e.workLock.Unlock()

	key := fmt.Sprintf("%s/%s", a.attributes.QuotaNamespace, a.attributes.QuotaName)
	e.queue.Add(key)

	if e.inProgress.Has(key) {
		e.dirtyWork[key] = append(e.dirtyWork[key], a)
		return
	}

	e.work[key] = append(e.work[key], a)
}

func (e *quotaEvaluator) completeWork(key string) {
	e.workLock.Lock()
	defer e.workLock.Unlock()

	e.queue.Done(key)
	e.work[key] = e.dirtyWork[key]
	delete(e.dirtyWork, key)
	e.inProgress.Delete(key)
}

func (e *quotaEvaluator) getWork() (string, []*admissionWaiter, bool) {
	uncastKey, shutdown := e.queue.Get()
	if shutdown {
		return "", []*admissionWaiter{}, shutdown
	}
	key := uncastKey.(string)

	e.workLock.Lock()
	defer e.workLock.Unlock()

	work := e.work[key]
	delete(e.work, key)
	delete(e.dirtyWork, key)
	e.inProgress.Insert(key)
	return key, work, false
}

// prettyPrint formats a resource list for usage in errors
// it outputs resources sorted in increasing order
func prettyPrint(item corev1.ResourceList) string {
	parts := []string{}
	keys := []string{}
	for key := range item {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := item[corev1.ResourceName(key)]
		constraint := key + "=" + value.String()
		parts = append(parts, constraint)
	}
	return strings.Join(parts, ",")
}

func prettyPrintResourceNames(a []corev1.ResourceName) string {
	values := []string{}
	for _, value := range a {
		values = append(values, string(value))
	}
	sort.Strings(values)
	return strings.Join(values, ",")
}
