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

package util

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	jsonpatch "github.com/evanphx/json-patch"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
)

// MergeCfg returns a merged interface. Value in new will
// override old's when both fields exist.
// It will throw an error if:
//  1. either of the inputs was nil;
//  2. inputs were not a pointer of the same json struct.
func MergeCfg(old, new interface{}) (interface{}, error) {
	if old == nil || new == nil {
		return nil, fmt.Errorf("invalid input, should not be empty")
	}

	if reflect.TypeOf(old).Kind() != reflect.Ptr || reflect.TypeOf(new).Kind() != reflect.Ptr {
		return nil, fmt.Errorf("invalid input, all types must be pointers to structs")
	}
	if reflect.TypeOf(old) != reflect.TypeOf(new) {
		return nil, fmt.Errorf("invalid input, should be the same type")
	}

	if data, err := json.Marshal(new); err != nil {
		return nil, err
	} else if err := json.Unmarshal(data, &old); err != nil {
		return nil, err
	}

	return old, nil
}

func MinInt64(i, j int64) int64 {
	if i < j {
		return i
	}
	return j
}

func MaxInt64(i, j int64) int64 {
	if i > j {
		return i
	}
	return j
}

func MinFloat64(i, j float64) float64 {
	if i < j {
		return i
	}
	return j
}

func MaxFloat64(i, j float64) float64 {
	if i > j {
		return i
	}
	return j
}

func RetryOnConflictOrTooManyRequests(fn func() error) error {
	return retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return apierrors.IsConflict(err) || apierrors.IsTooManyRequests(err)
	}, fn)
}

func RetryOnConflictOrTooManyRequestsOrConnectionClose(fn func() error) error {
	return retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return apierrors.IsConflict(err) || apierrors.IsTooManyRequests(err) || isErrorConnectionClosed(err)
	}, fn)
}

func isErrorConnectionClosed(err error) bool {
	errMsg := err.Error()
	return strings.Contains(errMsg, "http2: client connection force closed via ClientConn.Close") || net.IsProbableEOF(err) || net.IsConnectionReset(err)
}

// DefaultTransientBackoff is the recommended backoff for in-place retries of transient failures
// on the requests between the scheduler and the apiserver, e.g. network jitter on the load
// balancer in front of the apiserver. The retry window (4 attempts, ~1.4s) is designed to cover
// the second-scale jitter.
var DefaultTransientBackoff = wait.Backoff{
	Steps:    4,
	Duration: 200 * time.Millisecond,
	Factor:   2.0,
	Jitter:   0.1,
	Cap:      3 * time.Second,
}

// IsRetryableTransientError reports whether err is a transient failure worth an in-place retry
// for the requests between the scheduler and the apiserver. It covers both transient apiserver-side
// failures (e.g. throttling, temporarily unavailable) and network-level failures (e.g. connection
// refused/reset, broken pipe, http2 connection loss, probable EOF, transport timeout).
// Permanent failures (e.g. NotFound, Conflict, Forbidden, validation errors) return false.
func IsRetryableTransientError(err error) bool {
	if err == nil {
		return false
	}
	// Transient apiserver-side failures.
	if apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) ||
		apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err) || apierrors.IsUnexpectedServerError(err) {
		return true
	}
	// Transient network-level failures, e.g. network jitter between the scheduler and the apiserver.
	return net.IsConnectionRefused(err) || net.IsConnectionReset(err) || isErrorBrokenPipe(err) ||
		net.IsHTTP2ConnectionLost(err) || isErrorHTTP2ConnectionForceClosed(err) ||
		net.IsProbableEOF(err) || net.IsTimeout(err)
}

// isErrorBrokenPipe returns true if the given err is "broken pipe" error, e.g. writing to
// a connection that has been closed by the peer. It is the same class of transient
// failures as "connection reset by peer", but apimachinery util/net provides no helper
// for it, so keep the implementation aligned with net.IsConnectionReset.
func isErrorBrokenPipe(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EPIPE
	}
	return false
}

// errHTTP2ConnectionForceClosedMsg is the error returned by http2.ClientConn.Close()
// when the underlying connection is force closed mid-flight (a typical symptom of
// transient packet loss on the load balancer in front of the apiserver).
// The error is created via errors.New inside ClientConn.Close() (not a package-level var),
// so errors.Is/errors.As cannot match it. String matching is the only viable approach.
const errHTTP2ConnectionForceClosedMsg = "http2: client connection force closed via ClientConn.Close"

// isErrorHTTP2ConnectionForceClosed returns true if the given err is the http2
// errClientConnForceClosed, e.g. the underlying connection was closed while the
// request was in flight (a typical symptom of transient packet loss on the load
// balancer in front of the apiserver). Neither net.IsHTTP2ConnectionLost nor
// net.IsProbableEOF matches this message, so check it explicitly.
func isErrorHTTP2ConnectionForceClosed(err error) bool {
	return err != nil && strings.Contains(err.Error(), errHTTP2ConnectionForceClosedMsg)
}

// RetryOnTransientError retries fn in place on transient failures with DefaultTransientBackoff.
// See RetryOnTransientErrorWithBackoff for the detailed semantics.
func RetryOnTransientError(ctx context.Context, fn func() error) error {
	return RetryOnTransientErrorWithBackoff(ctx, DefaultTransientBackoff, fn)
}

// RetryOnTransientErrorWithBackoff retries fn in place on transient failures according to the
// given backoff. The ctx is checked before each attempt so that the retry loop fails fast when
// the scheduler loses leadership or shuts down, in which case retrying is pointless and
// continuing the request is no longer safe.
func RetryOnTransientErrorWithBackoff(ctx context.Context, backoff wait.Backoff, fn func() error) error {
	succeeded := false
	err := retry.OnError(backoff, IsRetryableTransientError, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(); err != nil {
			return err
		}
		succeeded = true
		return nil
	})
	if err == nil && !succeeded {
		// Never report success without a successful attempt: retry.OnError can return a nil
		// error even though no attempt succeeded. It substitutes the terminal error of the
		// backoff loop with the last retriable error, which stays nil when the loop ends
		// without recording one, e.g. interrupted by a canceled context before any
		// retryable failure.
		if err = ctx.Err(); err == nil {
			err = errors.New("retry on transient error interrupted before completion")
		}
	}
	return err
}

func GeneratePodPatch(oldPod, newPod *corev1.Pod) ([]byte, error) {
	oldData, err := json.Marshal(oldPod)
	if err != nil {
		return nil, err
	}

	newData, err := json.Marshal(newPod)
	if err != nil {
		return nil, err
	}
	return strategicpatch.CreateTwoWayMergePatch(oldData, newData, &corev1.Pod{})
}

func GeneratePodPatchWithUID(oldPod, newPod *corev1.Pod) ([]byte, error) {
	// For safely patch, generate with the object UID.
	// This ensures we will not patch the different object with the same name.
	oldPod = oldPod.DeepCopy()
	oldPod.UID = ""
	oldData, err := json.Marshal(oldPod)
	if err != nil {
		return nil, err
	}

	newData, err := json.Marshal(newPod)
	if err != nil {
		return nil, err
	}
	return strategicpatch.CreateTwoWayMergePatch(oldData, newData, &corev1.Pod{})
}

func PatchPod(ctx context.Context, clientset clientset.Interface, oldPod, newPod *corev1.Pod, subResources ...string) (*corev1.Pod, error) {
	if reflect.DeepEqual(oldPod, newPod) {
		return oldPod, nil
	}

	// generate patch bytes for the update
	patchBytes, err := GeneratePodPatch(oldPod, newPod)
	if err != nil {
		klog.V(5).InfoS("failed to generate pod patch", "pod", klog.KObj(oldPod), "err", err)
		return nil, err
	}
	if string(patchBytes) == "{}" { // nothing to patch
		return oldPod, nil
	}

	// patch with pod client
	patched, err := clientset.CoreV1().Pods(oldPod.Namespace).
		Patch(ctx, oldPod.Name, apimachinerytypes.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, subResources...)
	if err != nil {
		klog.V(5).InfoS("failed to patch pod", "pod", klog.KObj(oldPod), "patch", string(patchBytes), "err", err)
		return nil, err
	}
	klog.V(6).InfoS("successfully patch pod", "pod", klog.KObj(oldPod), "patch", string(patchBytes))
	return patched, nil
}

// PatchPodSafe patches the pod with the object UID for safety.
// This ensures we will not patch the different object with the same name.
func PatchPodSafe(ctx context.Context, clientset clientset.Interface, oldPod, newPod *corev1.Pod, subResources ...string) (*corev1.Pod, error) {
	if reflect.DeepEqual(oldPod, newPod) {
		return oldPod, nil
	}

	// generate patch bytes for the update
	patchBytes, err := GeneratePodPatchWithUID(oldPod, newPod)
	if err != nil {
		klog.V(5).InfoS("failed to generate pod patch", "pod", klog.KObj(oldPod), "err", err)
		return nil, err
	}

	// patch with pod client
	patched, err := clientset.CoreV1().Pods(oldPod.Namespace).
		Patch(ctx, oldPod.Name, apimachinerytypes.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, subResources...)
	if err != nil {
		klog.V(5).InfoS("failed to patch pod", "pod", klog.KObj(oldPod), "patch", string(patchBytes), "err", err)
		return nil, err
	}
	klog.V(6).InfoS("successfully patch pod", "pod", klog.KObj(oldPod), "patch", string(patchBytes))
	return patched, nil
}

func GenerateReservationPatch(oldReservation, newReservation *schedulingv1alpha1.Reservation) ([]byte, error) {
	oldData, err := json.Marshal(oldReservation)
	if err != nil {
		return nil, err
	}

	newData, err := json.Marshal(newReservation)
	if err != nil {
		return nil, err
	}
	return jsonpatch.CreateMergePatch(oldData, newData)
}

func GenerateReservationPatchWithUID(oldReservation, newReservation *schedulingv1alpha1.Reservation) ([]byte, error) {
	// For safely patch, generate with the object UID.
	// This ensures we will not patch the different object with the same name.
	oldReservation = oldReservation.DeepCopy()
	oldReservation.UID = ""
	oldData, err := json.Marshal(oldReservation)
	if err != nil {
		return nil, err
	}

	newData, err := json.Marshal(newReservation)
	if err != nil {
		return nil, err
	}
	return jsonpatch.CreateMergePatch(oldData, newData)
}

func PatchReservation(ctx context.Context, clientset koordinatorclientset.Interface, oldReservation, newReservation *schedulingv1alpha1.Reservation) (*schedulingv1alpha1.Reservation, error) {
	if reflect.DeepEqual(oldReservation, newReservation) {
		return oldReservation, nil
	}

	patchBytes, err := GenerateReservationPatch(oldReservation, newReservation)
	if err != nil {
		klog.V(5).InfoS("failed to generate reservation patch", "reservation", klog.KObj(oldReservation), "err", err)
		return nil, err
	}
	if string(patchBytes) == "{}" { // nothing to patch
		return oldReservation, nil
	}

	// NOTE: CRDs do not support strategy merge patch, so here falls back to merge patch.
	// link: https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/#advanced-features-and-flexibility
	patched, err := clientset.SchedulingV1alpha1().Reservations().
		Patch(ctx, oldReservation.Name, apimachinerytypes.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		klog.V(5).InfoS("failed to patch reservation", "reservation", klog.KObj(oldReservation), "patch", string(patchBytes), "err", err)
		return nil, err
	}
	klog.V(6).InfoS("successfully patch reservation", "reservation", klog.KObj(oldReservation), "patch", string(patchBytes))
	return patched, nil
}

// PatchReservationSafe patches the reservation with the object UID for safety.
// This ensures we will not patch the different object with the same name.
func PatchReservationSafe(ctx context.Context, clientset koordinatorclientset.Interface, oldReservation, newReservation *schedulingv1alpha1.Reservation) (*schedulingv1alpha1.Reservation, error) {
	if reflect.DeepEqual(oldReservation, newReservation) {
		return oldReservation, nil
	}

	patchBytes, err := GenerateReservationPatchWithUID(oldReservation, newReservation)
	if err != nil {
		klog.V(5).InfoS("failed to generate reservation patch", "reservation", klog.KObj(oldReservation), "err", err)
		return nil, err
	}

	// NOTE: CRDs do not support strategy merge patch, so here falls back to merge patch.
	// link: https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/#advanced-features-and-flexibility
	patched, err := clientset.SchedulingV1alpha1().Reservations().
		Patch(ctx, oldReservation.Name, apimachinerytypes.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		klog.V(5).InfoS("failed to patch reservation", "reservation", klog.KObj(oldReservation), "patch", string(patchBytes), "err", err)
		return nil, err
	}
	klog.V(6).InfoS("successfully patch reservation", "reservation", klog.KObj(oldReservation), "patch", string(patchBytes))
	return patched, nil
}

func GenerateNodePatch(oldNode, newNode *corev1.Node) ([]byte, error) {
	oldData, err := json.Marshal(oldNode)
	if err != nil {
		return nil, err
	}

	newData, err := json.Marshal(newNode)
	if err != nil {
		return nil, err
	}
	return strategicpatch.CreateTwoWayMergePatch(oldData, newData, &corev1.Node{})
}

func PatchNode(ctx context.Context, clientset clientset.Interface, oldNode, newNode *corev1.Node, subResources ...string) (*corev1.Node, error) {
	if reflect.DeepEqual(oldNode, newNode) {
		return oldNode, nil
	}

	// generate patch bytes for the update
	patchBytes, err := GenerateNodePatch(oldNode, newNode)
	if err != nil {
		klog.V(5).InfoS("failed to generate node patch", "node", klog.KObj(oldNode), "err", err)
		return nil, err
	}
	if string(patchBytes) == "{}" { // nothing to patch
		return oldNode, nil
	}

	// patch with node client
	patched, err := clientset.CoreV1().Nodes().
		Patch(ctx, oldNode.Name, apimachinerytypes.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, subResources...)
	if err != nil {
		klog.V(5).InfoS("failed to patch node", "node", klog.KObj(oldNode), "patch", string(patchBytes), "err", err)
		return nil, err
	}
	klog.V(6).InfoS("successfully patch node", "node", klog.KObj(oldNode), "patch", string(patchBytes))
	return patched, nil
}

func GetNamespacedName(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

func BoolToFloat64(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

func IsIn(arr []string, val string) bool {
	for _, cur := range arr {
		if cur == val {
			return true
		}
	}

	return false
}

// TODO: Replace this function with the standard library after go1.21+ version
func OnceValues(f func() ([]int, error)) func() ([]int, error) {
	var (
		once  sync.Once
		valid bool
		p     any
		r1    []int
		r2    error
	)
	g := func() {
		defer func() {
			p = recover()
			if !valid {
				panic(p)
			}
		}()
		r1, r2 = f()
		valid = true
	}
	return func() ([]int, error) {
		once.Do(g)
		if !valid {
			panic(p)
		}
		return r1, r2
	}
}
