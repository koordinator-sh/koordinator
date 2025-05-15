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
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

const Name = "colocationprofile"

var (
	ReconcileInterval    = 30 * time.Second
	MaxUpdatePodQPS      = 1.0
	MaxUpdatePodQPSBurst = 2
)

type Reconciler struct {
	client.Client
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme

	rateLimiter *rate.Limiter
}

func newReconciler(mgr ctrl.Manager) *Reconciler {
	return &Reconciler{
		Client:      mgr.GetClient(),
		Recorder:    mgr.GetEventRecorderFor(Name),
		Scheme:      mgr.GetScheme(),
		rateLimiter: rate.NewLimiter(rate.Limit(MaxUpdatePodQPS), MaxUpdatePodQPSBurst),
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

	podList, err := r.listPodsForProfile(profile)
	if err != nil {
		klog.ErrorS(err, "failed to list pods for ClusterColocationProfile", "profile", profile.Name)
		return ctrl.Result{Requeue: true}, err
	}
	if podList == nil {
		klog.V(6).InfoS("list no pod for ClusterColocationProfile, retry later", "profile", profile.Name)
		return ctrl.Result{RequeueAfter: ReconcileInterval}, nil
	}
	klog.V(5).InfoS("list pods for ClusterColocationProfile", "profile", profile.Name, "pods", len(podList.Items))

	count := 0
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
			klog.V(4).InfoS("skip update Pod by clusterColocationProfile", "profile", profile.Name, "pod", klog.KObj(pod))
			continue
		}
		// TODO: support rate limiter
		isUpdated, err := r.updatePodByClusterColocationProfile(context.TODO(), profile, pod)
		if err != nil {
			klog.ErrorS(err, "failed to patch pod for clusterColocationProfile", "profile", profile.Name, "pod", klog.KObj(pod))
			continue
		}
		if isUpdated {
			count++
		}
	}
	klog.V(4).InfoS("successfully update pods for clusterColocationProfile", "profile", profile.Name, "count", count)

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
	pflag.DurationVar(&ReconcileInterval, "colocation-profile-reconcile-interval", ReconcileInterval, "The interval to reconcile ClusterColocationProfile.")
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
