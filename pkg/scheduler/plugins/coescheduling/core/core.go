package core

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	informerv1 "k8s.io/client-go/informers/core/v1"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	pgclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	pginformer "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions/scheduling/v1alpha1"
	pglister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

type Status string

const (
	// PodGroupNotSpecified denotes no PodGroup is specified in the Pod spec.
	PodGroupNotSpecified Status = "PodGroup not specified"
	// PodGroupNotFound denotes the specified PodGroup in the Pod spec is
	// not found in API server.
	PodGroupNotFound Status = "PodGroup not found"
	Success          Status = "Success"
	Wait             Status = "Wait"
)

// Manager defines the interfaces for PodGroup management.
type Manager interface {
	PreFilter(context.Context, *corev1.Pod) error
	Permit(context.Context, *corev1.Pod) Status
	PostBind(context.Context, *corev1.Pod, string)
	GetPodGroup(*corev1.Pod) (string, *v1alpha1.PodGroup)
	GetCreationTimestamp(*corev1.Pod, time.Time) time.Time
	DeletePermittedPodGroup(string)
	CalculateAssignedPods(string, string) int
	ActivateSiblings(pod *corev1.Pod, state *framework.CycleState)
	Recover()
}

// PodGroupManager defines the scheduling operation called
type PodGroupManager struct {
	// pgClient is a podGroup client
	pgClient pgclientset.Interface
	// snapshotSharedLister is pod shared list
	snapshotSharedLister framework.SharedLister
	// scheduleTimeout is the default timeout for podgroup scheduling.
	// If podgroup's scheduleTimeoutSeconds is set, it will be used.
	scheduleTimeout *time.Duration
	// pgLister is podgroup lister
	pgLister pglister.PodGroupLister
	// podLister is pod lister
	podLister listerv1.PodLister
	// reserveResourcePercentage is the reserved resource for the max finished group, range (0,100]
	reserveResourcePercentage int32
	// cache stores gang info
	cache *GangCache
	sync.RWMutex
}

// NewPodGroupManager creates a new operation object.
func NewPodGroupManager(pgClient pgclientset.Interface, snapshotSharedLister framework.SharedLister, scheduleTimeout *time.Duration,
	pgInformer pginformer.PodGroupInformer, podInformer informerv1.PodInformer, args *schedulingconfig.GangArgs) *PodGroupManager {
	pgMgr := &PodGroupManager{
		pgClient:             pgClient,
		snapshotSharedLister: snapshotSharedLister,
		scheduleTimeout:      scheduleTimeout,
		pgLister:             pgInformer.Lister(),
		podLister:            podInformer.Lister(),
	}
	gangCache := NewGangCache(args, podInformer.Lister(), pgInformer.Lister())
	pgMgr.cache = gangCache
	return pgMgr
}
