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

package colocationprofile

import (
	"context"
	"flag"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/spf13/pflag"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

const Name = "colocationprofile"

var (
	ReconcileByDefault     = false
	ReconcileInterval      = 30 * time.Second
	ForceUpdatePodDuration = 5 * time.Minute
	ExpirePodCacheDuration = 10 * time.Minute
	MaxUpdatePodQPS        = 5.0
	MaxUpdatePodQPSBurst   = 10
)

type Reconciler struct {
	client.Client
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme

	rateLimiter    *rate.Limiter
	podUpdateCache gocache.Cache
}

func newReconciler(mgr ctrl.Manager) *Reconciler {
	return &Reconciler{
		Client:         mgr.GetClient(),
		Recorder:       mgr.GetEventRecorderFor(Name),
		Scheme:         mgr.GetScheme(),
		rateLimiter:    rate.NewLimiter(rate.Limit(MaxUpdatePodQPS), MaxUpdatePodQPSBurst),
		podUpdateCache: *gocache.New(ForceUpdatePodDuration, ExpirePodCacheDuration),
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	profile := &configv1alpha1.ClusterColocationProfile{}
	if err := r.Client.Get(ctx, req.NamespacedName, profile); err != nil {
		if !errors.IsNotFound(err) {
			klog.ErrorS(err, "failed to get tenantProfile", "profile", req.NamespacedName)
			return ctrl.Result{Requeue: true}, err
		}
		// not found
		return ctrl.Result{}, nil
	}

	if profile.DeletionTimestamp != nil { // skip for a terminating profile
		return ctrl.Result{}, nil
	}

	profileEnabled := extension.ShouldReconcileProfile(profile)
	klog.V(5).InfoS("reconcile for clusterColocationProfile",
		"profile", profile.Name, "defaultEnabled", ReconcileByDefault, "profileEnabled", profileEnabled)
	if !ReconcileByDefault && !profileEnabled {
		// should not handle the profile
		return ctrl.Result{}, nil
	}

	podList, err := r.listPodsForProfile(profile)
	if err != nil {
		klog.ErrorS(err, "failed to list pods for ClusterColocationProfile", "profile", profile.Name)
		return ctrl.Result{Requeue: true}, err
	}
	if podList == nil || len(podList.Items) <= 0 {
		klog.V(6).InfoS("list no pod for ClusterColocationProfile, retry later", "profile", profile.Name)
		return ctrl.Result{RequeueAfter: ReconcileInterval}, nil
	}
	klog.V(5).InfoS("list pods for ClusterColocationProfile", "profile", profile.Name, "pods", len(podList.Items))

	summary := newSummary(profile.Name)
	for i := range podList.Items {
		pod := &podList.Items[i]
		// NOTE: Only handle pending and unscheduled pods.
		if pod.Spec.NodeName != "" || pod.Status.Phase != corev1.PodPending {
			continue
		}
		skip, err := shouldSkipProfile(profile)
		if err != nil {
			klog.ErrorS(err, "failed to check skip profile for pod", "profile", profile.Name, "pod", klog.KObj(pod))
			return ctrl.Result{Requeue: true}, err
		}
		if skip {
			summary.Skipped++
			klog.V(5).InfoS("skip update Pod by clusterColocationProfile", "profile", profile.Name, "pod", klog.KObj(pod))
			continue
		}
		_, exists := r.podUpdateCache.Get(getPodUpdateKey(profile, pod))
		if exists {
			summary.Cached++
			klog.V(5).InfoS("skip update Pod by clusterColocationProfile, already updated", "profile", profile.Name, "pod", klog.KObj(pod))
			continue
		}
		summary.Desired++
		if !r.rateLimiter.Allow() { // rate limit the pod updates
			summary.RateLimited++
			klog.V(4).InfoS("abort update Pod by clusterColocationProfile, rate limiter is not allowed",
				"profile", profile.Name, "pod", klog.KObj(pod), "qps", r.rateLimiter.Limit(), "burst", r.rateLimiter.Burst())
			continue
		}
		isUpdated, err := r.updatePodByClusterColocationProfile(context.TODO(), profile, pod)
		if err != nil {
			klog.ErrorS(err, "failed to patch pod for clusterColocationProfile", "profile", profile.Name, "pod", klog.KObj(pod))
			continue
		}
		summary.Succeeded++
		if isUpdated {
			r.podUpdateCache.SetDefault(getPodUpdateKey(profile, pod), struct{}{})
			summary.Changed++
		}
	}
	klog.V(4).InfoS("successfully update pods for clusterColocationProfile",
		"profile", profile.Name, "allSucceeded", summary.IsAllSucceeded(), "summary", summary)

	// TODO: handle reservations
	return ctrl.Result{RequeueAfter: ReconcileInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1alpha1.ClusterColocationProfile{}).
		Named(Name).
		Complete(r)
}

func InitFlags(fs *flag.FlagSet) {
	pflag.BoolVar(&ReconcileByDefault, "colocation-profile-reconcile-by-default", ReconcileByDefault, "Whether the colocation-profile controller reconciles ClusterColocationProfile by default.")
	pflag.DurationVar(&ReconcileInterval, "colocation-profile-reconcile-interval", ReconcileInterval, "The interval to reconcile ClusterColocationProfile.")
	pflag.DurationVar(&ForceUpdatePodDuration, "colocation-profile-force-update-pod-duration", ForceUpdatePodDuration, "The duration for colocation-profile controller to force update pods.")
	pflag.DurationVar(&ExpirePodCacheDuration, "colocation-profile-expire-pod-cache-duration", ExpirePodCacheDuration, "The duration for colocation-profile controller to expire pod cache.")
	pflag.Float64Var(&MaxUpdatePodQPS, "colocation-profile-update-pod-qps", MaxUpdatePodQPS, "The QPS for colocation-profile controller to update pods.")
	pflag.IntVar(&MaxUpdatePodQPSBurst, "colocation-profile-update-pod-qps-burst", MaxUpdatePodQPSBurst, "The QPS burst for colocation-profile controller to update pods.")
}

func Add(mgr ctrl.Manager) error {
	if !utilfeature.DefaultMutableFeatureGate.Enabled(features.ColocationProfileController) {
		klog.InfoS("ColocationProfileController feature is disabled")
		return nil
	}

	klog.InfoS("ColocationProfileController is enabled, add the controller")
	reconciler := newReconciler(mgr)
	return reconciler.SetupWithManager(mgr)
}
