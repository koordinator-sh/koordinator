package podgroup

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

// PodGroupReconciler reconciles a PodGroup object
type PodGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=scheduling.sigs.k8s.io,resources=podgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=scheduling.sigs.k8s.io,resources=podgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=scheduling.sigs.k8s.io,resources=podgroups/finalizers,verbs=update

// Reconcile s logic is the same as Coescheduling's
// Aim to update the PodGroup Crd's information
func (r *PodGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx, "podGroup-reconciler", req.NamespacedName)
	pg := &v1alpha1.PodGroup{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, pg); err != nil {
		if !errors.IsNotFound(err) {
			klog.Errorf("failed to find podGroup: %v, error: %v", req.NamespacedName, err)
			return ctrl.Result{Requeue: true}, err
		} else {
			klog.Errorf("PodGroup: %v has been deleted", req.NamespacedName)
			return ctrl.Result{}, nil
		}
	}
	pgCopy := pg.DeepCopy()
	selector := labels.Set(map[string]string{v1alpha1.PodGroupLabel: pgCopy.Name}).AsSelector()
	pods := &v1.PodList{}
	err := r.Client.List(context.TODO(), pods, &client.ListOptions{LabelSelector: selector})
	if err != nil {
		klog.Errorf("PodGroupReconciler List pods belong to podGroup: %v error: %v", req.NamespacedName, err)
		return ctrl.Result{Requeue: true}, err
	}

	switch pgCopy.Status.Phase {
	case "":
		pgCopy.Status.Phase = v1alpha1.PodGroupPending
	case v1alpha1.PodGroupPending:
		if len(pods.Items) >= int(pg.Spec.MinMember) {
			pgCopy.Status.Phase = v1alpha1.PodGroupPreScheduling
			fillOccupiedObj(pgCopy, &pods.Items[0])
		}
	default:
		var (
			running   int32 = 0
			succeeded int32 = 0
			failed    int32 = 0
		)
		if len(pods.Items) != 0 {
			for _, pod := range pods.Items {
				switch pod.Status.Phase {
				case v1.PodRunning:
					running++
				case v1.PodSucceeded:
					succeeded++
				case v1.PodFailed:
					failed++
				}
			}
		}
		pgCopy.Status.Failed = failed
		pgCopy.Status.Succeeded = succeeded
		pgCopy.Status.Running = running

		if len(pods.Items) == 0 {
			pgCopy.Status.Phase = v1alpha1.PodGroupPending
			break
		}

		if pgCopy.Status.Scheduled >= pgCopy.Spec.MinMember && pgCopy.Status.Phase == v1alpha1.PodGroupScheduling {
			pgCopy.Status.Phase = v1alpha1.PodGroupScheduled
		}

		if pgCopy.Status.Succeeded+pgCopy.Status.Running >= pg.Spec.MinMember && pgCopy.Status.Phase == v1alpha1.PodGroupScheduled {
			pgCopy.Status.Phase = v1alpha1.PodGroupRunning
		}
		// Final state of pod group
		if pgCopy.Status.Failed != 0 && pgCopy.Status.Failed+pgCopy.Status.Running+pgCopy.Status.Succeeded >= pg.Spec.
			MinMember {
			pgCopy.Status.Phase = v1alpha1.PodGroupFailed
		}
		if pgCopy.Status.Succeeded >= pg.Spec.MinMember {
			pgCopy.Status.Phase = v1alpha1.PodGroupFinished
		}
	}
	if !reflect.DeepEqual(pg, pgCopy) {
		err = r.Client.Patch(context.TODO(), pgCopy, client.MergeFrom(pg))
		if err != nil {
			klog.Errorf("PodGroup: %v ,controller patch error: %v", req.NamespacedName, err)
			return ctrl.Result{Requeue: true}, err
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PodGroup{}).
		Watches(&source.Kind{Type: &v1.Pod{}}, &EnqueueRequestForPod{}).
		Watches(&source.Kind{Type: &v1alpha1.PodGroup{}}, &EnqueueRequestForPodGroup{}).
		Complete(r)
}

func fillOccupiedObj(pg *v1alpha1.PodGroup, pod *v1.Pod) {
	var refs []string
	for _, ownerRef := range pod.OwnerReferences {
		refs = append(refs, fmt.Sprintf("%s/%s", pod.Namespace, ownerRef.Name))
	}
	if len(pg.Status.OccupiedBy) == 0 {
		return
	}
	if len(refs) != 0 {
		sort.Strings(refs)
		pg.Status.OccupiedBy = strings.Join(refs, ",")
	}
}
