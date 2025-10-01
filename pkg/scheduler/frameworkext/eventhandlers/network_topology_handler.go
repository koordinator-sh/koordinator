package eventhandlers

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
)

// syncedPollPeriod controls how often you look at the status of your sync funcs
var syncedPollPeriod = 100 * time.Millisecond

func AddNetworkTopologyRelated(
	ctx context.Context,
	networkTopologyManager networktopology.TreeManager,
	koordSharedInformerFactory koordinatorinformers.SharedInformerFactory,
	sharedInformerFactory informers.SharedInformerFactory) error {
	koordSharedInformerFactory.WaitForCacheSync(ctx.Done())
	networkTopologyHandle, err := koordSharedInformerFactory.Scheduling().V1alpha1().ClusterNetworkTopologies().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			clusterNetworkTopology, ok := obj.(*schedulingv1alpha1.ClusterNetworkTopology)
			if !ok {
				return
			}
			networkTopologyManager.RefreshTree(clusterNetworkTopology)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			clusterNetworkTopology, ok := newObj.(*schedulingv1alpha1.ClusterNetworkTopology)
			if !ok {
				return
			}
			networkTopologyManager.RefreshTree(clusterNetworkTopology)
		},
		DeleteFunc: func(obj interface{}) {
			var clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology
			switch t := obj.(type) {
			case *schedulingv1alpha1.ClusterNetworkTopology:
				clusterNetworkTopology = t
			case cache.DeletedFinalStateUnknown:
				var ok bool
				clusterNetworkTopology, ok = t.Obj.(*schedulingv1alpha1.ClusterNetworkTopology)
				if !ok {
					return
				}
			default:
				return
			}
			networkTopologyManager.DeleteTree(clusterNetworkTopology)
		},
	})
	if err != nil {
		return err
	}
	_ = wait.PollUntilContextCancel(ctx, syncedPollPeriod, true, func(ctx context.Context) (done bool, err error) {
		if !networkTopologyHandle.HasSynced() {
			return false, nil
		}
		return true, nil
	})
	sharedInformerFactory.WaitForCacheSync(ctx.Done())
	nodeHandle, err := sharedInformerFactory.Core().V1().Nodes().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			node, ok := obj.(*corev1.Node)
			if !ok {
				return
			}
			networkTopologyManager.AddNode(node)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNode, ok := oldObj.(*corev1.Node)
			if !ok {
				return
			}
			node, ok := newObj.(*corev1.Node)
			if !ok {
				return
			}
			networkTopologyManager.UpdateNode(oldNode, node)
		},
		DeleteFunc: func(obj interface{}) {
			var node *corev1.Node
			switch t := obj.(type) {
			case *corev1.Node:
				node = t
			case cache.DeletedFinalStateUnknown:
				var ok bool
				node, ok = t.Obj.(*corev1.Node)
				if !ok {
					return
				}
			default:
				return
			}
			networkTopologyManager.DeleteNode(node)
		},
	})
	if err != nil {
		return err
	}
	_ = wait.PollUntilContextCancel(ctx, syncedPollPeriod, true, func(ctx context.Context) (done bool, err error) {
		if !nodeHandle.HasSynced() {
			return false, nil
		}
		return true, nil
	})
	return nil
}
