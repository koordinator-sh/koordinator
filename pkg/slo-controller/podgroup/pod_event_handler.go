package podgroup

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coescheduling"
)

var _ handler.EventHandler = &EnqueueRequestForPod{}

type EnqueueRequestForPod struct{}

func (n EnqueueRequestForPod) Create(e event.CreateEvent, q workqueue.RateLimitingInterface) {
	if pod, ok := e.Object.(*corev1.Pod); !ok {
		return
	} else {
		pgName := coescheduling.GetGangNameByPod(pod)
		if pgName == "" {
			return
		}
		if coescheduling.PgFromAnnotation(pod) {
			return
		}
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      pgName,
				Namespace: pod.Namespace,
			},
		})
	}
}

func (n EnqueueRequestForPod) Update(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	new := e.ObjectNew
	if pod, ok := new.(*corev1.Pod); !ok {
		return
	} else {
		pgName := coescheduling.GetGangNameByPod(pod)
		if pgName == "" {
			return
		}
		if coescheduling.PgFromAnnotation(pod) {
			return
		}
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      pgName,
				Namespace: pod.Namespace,
			},
		})
	}
}

func (n EnqueueRequestForPod) Delete(e event.DeleteEvent, q workqueue.RateLimitingInterface) {}

func (n EnqueueRequestForPod) Generic(event event.GenericEvent, limitingInterface workqueue.RateLimitingInterface) {
}
