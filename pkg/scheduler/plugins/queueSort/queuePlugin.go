package queueSort

import (
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/gang"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// QueueSortPlugin
//We design the QueueSortPlugin plugin to implement the QueueSort extension point separately,
//so that we can integrate queue sort logic of all plugins, and register them at one time.
type QueueSortPlugin struct {
	frameworkHandler framework.Handle
}

var _ framework.QueueSortPlugin = &QueueSortPlugin{}

const (
	// Name is the name of the plugin used in Registry and configurations.
	Name = "QueueSort"
)

// New initializes and returns a new  plugin.
func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	return QueueSortPlugin{
		frameworkHandler: handle,
	}, nil
}
func (qs QueueSortPlugin) Name() string { return Name }

//Less is used to sort pods in the scheduling queue in the following order.
//Firstly, compare the priorities of the two pods, the higher priority is at the front of the queue.
//Secondly, compare creationTimestamp of two pods, if pod belongs to a Gang, then we compare creationTimestamp of the Gang, the one created first will be at the front of the queue.
//Finally, compare pod's namespace, if pod belongs to a Gang, then we compare Gang name.
func (qs *QueueSortPlugin) Less(podInfo1, podInfo2 *framework.QueuedPodInfo) bool {
	prio1 := corev1.PodPriority(podInfo1.Pod)
	prio2 := corev1.PodPriority(podInfo2.Pod)
	if prio1 != prio2 {
		return prio1 > prio2
	}

	creationTime1 := gang.GlobalGangPlugin.GetCreatTime(podInfo1)
	creationTime2 := gang.GlobalGangPlugin.GetCreatTime(podInfo2)
	if creationTime1.Equal(creationTime2) {
		return gang.GetNamespacedName(podInfo1.Pod) < gang.GetNamespacedName(podInfo2.Pod)
	}
	return creationTime1.Before(creationTime2)
}
