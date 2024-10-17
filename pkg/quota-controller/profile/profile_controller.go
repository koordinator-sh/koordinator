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

package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"reflect"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	nodeutil "k8s.io/kubernetes/pkg/util/node"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	schedv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

const Name = "quotaprofile"

const (
	ReasonCreateQuotaFailed = "CreateQuotaFailed"
	ReasonUpdateQuotaFailed = "UpdateQuotaFailed"
)

var resourceDecorators = []func(profile *v1alpha1.ElasticQuotaProfile, total corev1.ResourceList){
	DecorateResourceByResourceRatio,
}

func decorateTotalResource(profile *v1alpha1.ElasticQuotaProfile, total corev1.ResourceList) {
	for _, fn := range resourceDecorators {
		fn(profile, total)
	}
}

// QuotaProfileReconciler reconciles a QuotaProfile object
type QuotaProfileReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=scheduling.sigs.k8s.io,resources=elasticquotas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=quota.koordinator.sh,resources=elasticquotaprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=quota.koordinator.sh,resources=elasticquotaprofiles/status,verbs=get;update;patch

func (r *QuotaProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// reconcile for 2 things:
	//   1. ensuring the Quota exists if the QuotaProfile exists
	//   2. update Quota Spec
	_ = log.FromContext(ctx, "quota-profile-reconciler", req.NamespacedName)

	// get the profile
	profile := &v1alpha1.ElasticQuotaProfile{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, profile)
	if err != nil {
		if !errors.IsNotFound(err) {
			klog.Errorf("failed to find profile %v, error: %v", req.NamespacedName, err)
			return ctrl.Result{Requeue: true}, err
		}
		return ctrl.Result{}, nil
	}

	quotaTreeID := profile.Labels[extension.LabelQuotaTreeID]
	if quotaTreeID == "" {
		// generate quota tree id
		quotaTreeID = hash(fmt.Sprintf("%s/%s", profile.Namespace, profile.Name))
		if profile.Labels == nil {
			profile.Labels = make(map[string]string)
		}
		profile.Labels[extension.LabelQuotaTreeID] = quotaTreeID
		err := r.Client.Update(context.TODO(), profile)
		if err != nil {
			klog.Errorf("failed to update profile tree id: %v, error: %v", req.NamespacedName, err)
			return ctrl.Result{Requeue: true}, err
		}
	}

	quotaExist := true
	quota := &schedv1alpha1.ElasticQuota{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: profile.Namespace, Name: profile.Spec.QuotaName}, quota)
	if err != nil {
		if errors.IsNotFound(err) {
			quota = &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: profile.Namespace,
					Name:      profile.Spec.QuotaName,
				},
			}
			quotaExist = false
		} else {
			return ctrl.Result{Requeue: true, RequeueAfter: 1 * time.Second}, err
		}
	}
	oldQuota := quota.DeepCopy()

	selector, err := metav1.LabelSelectorAsSelector(profile.Spec.NodeSelector)
	if err != nil {
		klog.Errorf("failed to convert profile %v nodeSelector, error: %v", req.NamespacedName, err)
		return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, err
	}
	nodeList := &corev1.NodeList{}
	if err := r.Client.List(context.TODO(), nodeList, &client.ListOptions{LabelSelector: selector}, utilclient.DisableDeepCopy); err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	// TODO: consider node status.
	totalResource := corev1.ResourceList{}
	unschedulableResource := corev1.ResourceList{}
	for _, node := range nodeList.Items {
		totalResource = quotav1.Add(totalResource, GetNodeAllocatable(node))
		if node.Spec.Unschedulable || !nodeutil.IsNodeReady(&node) {
			unschedulableResource = quotav1.Add(unschedulableResource, GetNodeAllocatable(node))
		}
	}

	decorateTotalResource(profile, totalResource)
	decorateTotalResource(profile, unschedulableResource)

	resourceKeys := []string{"cpu", "memory"}
	raw, ok := profile.Annotations[extension.AnnotationResourceKeys]
	if ok {
		err := json.Unmarshal([]byte(raw), &resourceKeys)
		if err != nil {
			klog.Warningf("failed unmarshal quota.scheduling.koordinator.sh/resource-keys %v", raw)
		}
	}

	min := corev1.ResourceList{}
	max := corev1.ResourceList{}
	for _, key := range resourceKeys {
		resourceName := corev1.ResourceName(key)
		quantity, ok := totalResource[resourceName]
		if !ok {
			quantity = *resource.NewQuantity(0, resource.DecimalSI)
		}

		min[resourceName] = quantity
		max[resourceName] = *resource.NewQuantity(math.MaxInt64/2000, resource.DecimalSI)
	}

	// update min and max
	quota.Spec.Min = min
	quota.Spec.Max = max

	if quota.Labels == nil {
		quota.Labels = make(map[string]string)
	}
	// update quota label
	quota.Labels[extension.LabelQuotaProfile] = profile.Name
	if profile.Spec.QuotaLabels != nil {
		for k, v := range profile.Spec.QuotaLabels {
			quota.Labels[k] = v
		}
	}
	// update quota tree id
	quota.Labels[extension.LabelQuotaTreeID] = quotaTreeID

	// update quota root label
	quota.Labels[extension.LabelQuotaIsRoot] = "true"

	// update total resource
	data, err := json.Marshal(totalResource)
	if err != nil {
		klog.Errorf("failed marshal total resources, err: %v", err)
		return ctrl.Result{Requeue: true}, err
	}
	if quota.Annotations == nil {
		quota.Annotations = make(map[string]string)
	}
	quota.Annotations[extension.AnnotationTotalResource] = string(data)

	// update unschedulable resource
	data, err = json.Marshal(unschedulableResource)
	if err != nil {
		klog.Errorf("failed marshal unschedulable resources, err: %v", err)
		return ctrl.Result{Requeue: true}, err
	}
	quota.Annotations[extension.AnnotationUnschedulableResource] = string(data)

	if !quotaExist {
		err = r.Client.Create(context.TODO(), quota)
		if err != nil {
			r.Recorder.Eventf(profile, "Warning", ReasonCreateQuotaFailed, "failed to create quota, err: %s", err)
			klog.Errorf("failed create quota for profile %v, error: %v", req.NamespacedName, err)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
	} else {
		if !reflect.DeepEqual(quota.Labels, oldQuota.Labels) || !reflect.DeepEqual(quota.Annotations, oldQuota.Annotations) || !reflect.DeepEqual(quota.Spec, oldQuota.Spec) {
			err = r.Client.Update(context.TODO(), quota)
			if err != nil {
				r.Recorder.Eventf(profile, "Warning", ReasonUpdateQuotaFailed, "failed to update quota, err: %s", err)
				klog.Errorf("failed update quota for profile %v, error: %v", req.NamespacedName, err)
				return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
			}
		}
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func Add(mgr ctrl.Manager) error {
	reconciler := QuotaProfileReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("quotaprofile-controller"),
	}
	return reconciler.SetupWithManager(mgr)
}

func (r *QuotaProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ElasticQuotaProfile{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named(Name).
		Complete(r)
}

func MultiplyQuantity(value resource.Quantity, resName corev1.ResourceName, ratio float64) resource.Quantity {
	var q resource.Quantity
	switch resName {
	case corev1.ResourceCPU:
		v := int64(float64(value.MilliValue()) * ratio)
		q = *resource.NewMilliQuantity(v, resource.DecimalSI)
	case corev1.ResourceMemory:
		v := int64(float64(value.Value()) * ratio)
		q = *resource.NewQuantity(v, resource.BinarySI)
	default:
		v := int64(float64(value.Value()) * ratio)
		q = *resource.NewQuantity(v, resource.DecimalSI)
	}
	return q
}

func hash(s string) string {
	h := fnv.New64a()
	h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 10)
}

func DecorateResourceByResourceRatio(profile *v1alpha1.ElasticQuotaProfile, total corev1.ResourceList) {
	if profile.Spec.ResourceRatio == nil {
		return
	}

	ratio := 1.0
	val, err := strconv.ParseFloat(*profile.Spec.ResourceRatio, 64)
	if err == nil && val > 0 && val <= 1.0 {
		ratio = val
	}

	for resourceName, quantity := range total {
		total[resourceName] = MultiplyQuantity(quantity, resourceName, ratio)
	}
}
