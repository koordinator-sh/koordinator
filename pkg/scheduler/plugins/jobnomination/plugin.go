package jobnomination

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/component-helpers/resource"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	Name = "JobNomination"
	// defaultRetentionDuration is the default time retained resources are kept
	defaultRetentionDuration = 2 * time.Minute
)

// Nomination tracks retained resources for a failed pod on a specific node.
type Nomination struct {
	Resources  *framework.Resource
	Expiration time.Time
	PodUID     types.UID
}

// JobNomination is a plugin that retains resources for failed Job pods.
type JobNomination struct {
	handle            fwktype.Handle
	retentionDuration time.Duration

	// lock protects the cache
	lock sync.RWMutex
	// cache maps JobID -> NodeName -> list of Nominations (since multiple pods of same job can fail on same node)
	cache map[string]map[string][]*Nomination
}

var _ fwktype.PreFilterPlugin = &JobNomination{}
var _ fwktype.FilterPlugin = &JobNomination{}
var _ fwktype.ScorePlugin = &JobNomination{}
var _ fwktype.ReservePlugin = &JobNomination{}

func New(args runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
	jn := &JobNomination{
		handle:            handle,
		retentionDuration: defaultRetentionDuration,
		cache:             make(map[string]map[string][]*Nomination),
	}

	podInformer := handle.SharedInformerFactory().Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: jn.updatePod,
		DeleteFunc: jn.deletePod,
	})

	go jn.cleanupRoutine(context.Background())

	return jn, nil
}

func (jn *JobNomination) Name() string {
	return Name
}

func getJobID(pod *corev1.Pod) string {
	gangName := extension.GetGangName(pod)
	if gangName != "" {
		return gangName
	}
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return string(ref.UID)
		}
	}
	return ""
}

func isPodFailed(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodFailed
}

func (jn *JobNomination) updatePod(oldObj, newObj interface{}) {
	oldPod, ok := oldObj.(*corev1.Pod)
	if !ok {
		return
	}
	newPod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}
	
	if !isPodFailed(oldPod) && isPodFailed(newPod) {
		jn.trackPodRetention(newPod)
	}
}

func (jn *JobNomination) deletePod(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		pod, ok = tombstone.Obj.(*corev1.Pod)
		if !ok {
			return
		}
	}
	jn.trackPodRetention(pod)
}

func (jn *JobNomination) trackPodRetention(pod *corev1.Pod) {
	jobID := getJobID(pod)
	if jobID == "" {
		return
	}
	nodeName := pod.Spec.NodeName
	if nodeName == "" {
		return
	}

	requests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
	res := framework.NewResource(requests)
	if res.MilliCPU == 0 && res.Memory == 0 {
		return
	}

	jn.lock.Lock()
	defer jn.lock.Unlock()

	if jn.cache[jobID] == nil {
		jn.cache[jobID] = make(map[string][]*Nomination)
	}

	for _, nom := range jn.cache[jobID][nodeName] {
		if nom.PodUID == pod.UID {
			return
		}
	}

	jn.cache[jobID][nodeName] = append(jn.cache[jobID][nodeName], &Nomination{
		Resources:  res,
		Expiration: time.Now().Add(jn.retentionDuration),
		PodUID:     pod.UID,
	})
	klog.V(4).Infof("Tracked retention for Job %s Pod %s on Node %s", jobID, pod.Name, nodeName)
}

func (jn *JobNomination) cleanupRoutine(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			jn.cleanup()
		case <-ctx.Done():
			return
		}
	}
}

func (jn *JobNomination) cleanup() {
	jn.lock.Lock()
	defer jn.lock.Unlock()

	now := time.Now()
	for jobID, nodes := range jn.cache {
		for nodeName, noms := range nodes {
			var active []*Nomination
			for _, nom := range noms {
				if now.Before(nom.Expiration) {
					active = append(active, nom)
				}
			}
			if len(active) > 0 {
				nodes[nodeName] = active
			} else {
				delete(nodes, nodeName)
			}
		}
		if len(nodes) == 0 {
			delete(jn.cache, jobID)
		}
	}
}

func (jn *JobNomination) PreFilter(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodes []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	return nil, fwktype.NewStatus(fwktype.Success)
}

func (jn *JobNomination) PreFilterExtensions() fwktype.PreFilterExtensions {
	return nil
}

func (jn *JobNomination) Filter(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeInfo fwktype.NodeInfo) *fwktype.Status {
	nodeName := nodeInfo.Node().Name
	jobID := getJobID(pod)

	jn.lock.RLock()
	defer jn.lock.RUnlock()

	retainedByOthers := framework.NewResource(corev1.ResourceList{})
	now := time.Now()

	for trackedJobID, nodes := range jn.cache {
		if trackedJobID == jobID {
			continue
		}
		noms, ok := nodes[nodeName]
		if !ok {
			continue
		}
		for _, nom := range noms {
			if now.Before(nom.Expiration) {
				retainedByOthers.MilliCPU += nom.Resources.MilliCPU
				retainedByOthers.Memory += nom.Resources.Memory
				retainedByOthers.EphemeralStorage += nom.Resources.EphemeralStorage
				retainedByOthers.AllowedPodNumber += nom.Resources.AllowedPodNumber
			}
		}
	}

	if retainedByOthers.MilliCPU == 0 && retainedByOthers.Memory == 0 {
		return fwktype.NewStatus(fwktype.Success)
	}

	allocatable := nodeInfo.GetAllocatable().(*framework.Resource)
	requested := nodeInfo.GetRequested().(*framework.Resource)

	availableCPU := allocatable.MilliCPU - requested.MilliCPU - retainedByOthers.MilliCPU
	availableMem := allocatable.Memory - requested.Memory - retainedByOthers.Memory

	podRequests := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
	podRes := framework.NewResource(podRequests)

	if podRes.MilliCPU > availableCPU || podRes.Memory > availableMem {
		return fwktype.NewStatus(fwktype.Unschedulable, "Node resources retained by other jobs")
	}

	return fwktype.NewStatus(fwktype.Success)
}

func (jn *JobNomination) Score(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeInfo fwktype.NodeInfo) (int64, *fwktype.Status) {
	jobID := getJobID(pod)
	if jobID == "" {
		return 0, fwktype.NewStatus(fwktype.Success)
	}

	nodeName := nodeInfo.Node().Name

	jn.lock.RLock()
	defer jn.lock.RUnlock()

	nodes, ok := jn.cache[jobID]
	if !ok {
		return 0, fwktype.NewStatus(fwktype.Success)
	}

	noms, ok := nodes[nodeName]
	if !ok || len(noms) == 0 {
		return 0, fwktype.NewStatus(fwktype.Success)
	}

	now := time.Now()
	for _, nom := range noms {
		if now.Before(nom.Expiration) {
			return 100, fwktype.NewStatus(fwktype.Success)
		}
	}

	return 0, fwktype.NewStatus(fwktype.Success)
}

func (jn *JobNomination) ScoreExtensions() fwktype.ScoreExtensions {
	return nil
}

func (jn *JobNomination) Reserve(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	jobID := getJobID(pod)
	if jobID == "" {
		return fwktype.NewStatus(fwktype.Success)
	}

	jn.lock.Lock()
	defer jn.lock.Unlock()

	nodes, ok := jn.cache[jobID]
	if !ok {
		return fwktype.NewStatus(fwktype.Success)
	}

	noms, ok := nodes[nodeName]
	if !ok || len(noms) == 0 {
		return fwktype.NewStatus(fwktype.Success)
	}

	now := time.Now()
	var remaining []*Nomination
	consumed := false
	for _, nom := range noms {
		if !consumed && now.Before(nom.Expiration) {
			consumed = true
			continue
		}
		remaining = append(remaining, nom)
	}

	if len(remaining) > 0 {
		nodes[nodeName] = remaining
	} else {
		delete(nodes, nodeName)
	}

	if len(nodes) == 0 {
		delete(jn.cache, jobID)
	}

	return fwktype.NewStatus(fwktype.Success)
}

func (jn *JobNomination) Unreserve(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeName string) {
}
