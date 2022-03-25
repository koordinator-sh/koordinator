package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

func init() {
	prometheus.MustRegister(CommonCollectors...)
}

const (
	KoordletSubsystem = "koordlet"

	NodeKey = "node"

	StatusKey     = "status"
	StatusSucceed = "succeed"
	StatusFailed  = "failed"

	EvictionReasonKey = "reason"
	BESuppressTypeKey = "type"
)

var (
	NodeName string
	Node     *corev1.Node

	nodeLock sync.RWMutex
)

// Register registers the metrics with the node object
func Register(node *corev1.Node) {
	nodeLock.Lock()
	defer nodeLock.Unlock()

	if node != nil {
		NodeName = node.Name
	} else {
		NodeName = ""
		klog.Warning("register nil node for metrics")
	}
	Node = node
}

func genNodeLabels() prometheus.Labels {
	nodeLock.RLock()
	defer nodeLock.RUnlock()
	if Node == nil {
		return nil
	}

	return prometheus.Labels{
		NodeKey: NodeName,
	}
}
