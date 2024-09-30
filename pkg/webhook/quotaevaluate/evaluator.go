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
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	apiresource "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/utils/clock"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
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

	e.checkQuota(quota, admissionAttributes, 3)
}

func (e *quotaEvaluator) checkQuota(quota *v1alpha1.ElasticQuota, admissionAttributes []*admissionWaiter, remainingRetries int) {
	// yet another copy to compare against originals to see if we actually have deltas
	originalQuota := quota.DeepCopy()
	originChildRequest, err := extension.GetChildRequest(originalQuota)
	if err != nil {
		klog.Warningf("failed go get child request %v/%v, err: %v", quota.Namespace, quota.Name, err)
	}
	childRequest, err := extension.GetChildRequest(quota)
	if err != nil {
		klog.Warningf("failed go get child request %v/%v, err: %v", quota.Namespace, quota.Name, err)
	}

	changed := false
	for i := range admissionAttributes {
		admissionAttribute := admissionAttributes[i]
		newQuota, err := e.checkRequest(quota, admissionAttribute.attributes)
		if err != nil {
			admissionAttribute.result = err
			continue
		}

		newChildRequest, err := extension.GetChildRequest(newQuota)
		if err != nil {
			klog.Warningf("failed go get child request %v/%v, err: %v", quota.Namespace, quota.Name, err)
		}
		if !quotav1.Equals(childRequest, newChildRequest) {
			changed = true
		} else {
			admissionAttribute.result = nil
		}

		childRequest = newChildRequest
		quota = newQuota
	}

	if !changed {
		return
	}

	var updateErr error
	if !quotav1.Equals(originChildRequest, childRequest) {
		updateErr = e.quotaAccessor.UpdateQuotaStatus(quota)
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

	e.checkQuota(newQuota, admissionAttributes, remainingRetries-1)
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

func (e *quotaEvaluator) checkRequest(quota *v1alpha1.ElasticQuota, a *Attributes) (*v1alpha1.ElasticQuota, error) {
	if !e.Handles(a) {
		return quota, nil
	}

	if len(quotav1.Intersection(quotav1.ResourceNames(quota.Spec.Max), podResources)) == 0 {
		return quota, nil
	}

	deltaUsage, err := PodUsageFunc(a.Pod, clock.RealClock{})
	if err != nil {
		return quota, err
	}

	deltaUsage = quotav1.RemoveZeros(deltaUsage)
	if len(deltaUsage) == 0 {
		return quota, nil
	}

	hardResources := quotav1.ResourceNames(quota.Spec.Max)
	requestedUsage := quotav1.Mask(deltaUsage, hardResources)
	requestedUsage = quotav1.RemoveZeros(requestedUsage)
	if len(requestedUsage) == 0 {
		return quota, nil
	}

	quotaCopy := quota.DeepCopy()
	used, err := extension.GetChildRequest(quotaCopy)
	if err != nil {
		return nil, err
	}
	admission, err := GetQuotaAdmission(quotaCopy)
	if err != nil {
		return nil, err
	}

	newUsage := quotav1.Add(used, requestedUsage)
	maskedNewUsage := quotav1.Mask(newUsage, quotav1.ResourceNames(requestedUsage))

	if allowed, exceeded := quotav1.LessThanOrEqual(maskedNewUsage, admission); !allowed {
		failedRequestedUsage := quotav1.Mask(requestedUsage, exceeded)
		failedUsed := quotav1.Mask(used, exceeded)
		failedHard := quotav1.Mask(admission, exceeded)
		return quota, fmt.Errorf("exceeded quota: %s/%s, requested: %s, used: %s, limited: %s",
			quota.Namespace, quota.Name,
			prettyPrint(failedRequestedUsage),
			prettyPrint(failedUsed),
			prettyPrint(failedHard))
	}

	data, err := json.Marshal(newUsage)
	if err != nil {
		return nil, err
	}
	if quotaCopy.Annotations == nil {
		quotaCopy.Annotations = make(map[string]string)
	}
	quotaCopy.Annotations[extension.AnnotationChildRequest] = string(data)

	return quotaCopy, nil
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
