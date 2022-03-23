package noderesource

import (
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

var _ handler.EventHandler = &EnqueueRequestForConfigMap{}

type EnqueueRequestForConfigMap struct {
	client.Client
	config *Config
}

// TODO

func (n *EnqueueRequestForConfigMap) Create(e event.CreateEvent, q workqueue.RateLimitingInterface) {
}

func (n *EnqueueRequestForConfigMap) Update(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
}

func (n *EnqueueRequestForConfigMap) Delete(e event.DeleteEvent, q workqueue.RateLimitingInterface) {
}

func (n *EnqueueRequestForConfigMap) Generic(e event.GenericEvent, q workqueue.RateLimitingInterface) {
}
