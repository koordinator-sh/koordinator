package podgroup

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

var _ handler.EventHandler = &EnqueueRequestForPodGroup{}

type EnqueueRequestForPodGroup struct {
}

func (n EnqueueRequestForPodGroup) Create(e event.CreateEvent, q workqueue.RateLimitingInterface) {
	if pg, ok := e.Object.(*v1alpha1.PodGroup); !ok {
		return
	} else {
		pgName := pg.Name
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      pgName,
				Namespace: pg.Namespace,
			},
		})
	}
}

func (n EnqueueRequestForPodGroup) Update(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	new := e.ObjectNew
	if pg, ok := new.(*v1alpha1.PodGroup); !ok {
		return
	} else {
		gangName := pg.Name
		if gangName == "" {
			return
		}
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      gangName,
				Namespace: pg.Namespace,
			},
		})
	}
}

func (n EnqueueRequestForPodGroup) Delete(e event.DeleteEvent, q workqueue.RateLimitingInterface) {}

func (n EnqueueRequestForPodGroup) Generic(e event.GenericEvent, q workqueue.RateLimitingInterface) {}
