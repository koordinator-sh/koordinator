package controller

import (
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/core"
)

type PodGroupBuffer struct {
	bufferedGangGroups map[string]bool
	sync.RWMutex

	podGroupManager  core.Manager
	podLister        corelister.PodLister
	activatePodsFunc func(pods map[string]*corev1.Pod)
}

func NewPodGroupBuffer(podGroupManager core.Manager, podLister corelister.PodLister, activatePodsFunc func(pods map[string]*corev1.Pod)) *PodGroupBuffer {
	return &PodGroupBuffer{
		bufferedGangGroups: make(map[string]bool),
		podGroupManager:    podGroupManager,
		podLister:          podLister,
		activatePodsFunc:   activatePodsFunc,
	}
}

func (b *PodGroupBuffer) BufferGangGroupIfNot(gangGroupID string) {
	if !k8sfeature.DefaultFeatureGate.Enabled(features.BufferGangGroups) {
		return
	}
	b.Lock()
	defer b.Unlock()
	b.bufferedGangGroups[gangGroupID] = false
}

func (b *PodGroupBuffer) Name() string {
	return "PodGroupBuffer"
}

func (b *PodGroupBuffer) Start() {
	if !k8sfeature.DefaultFeatureGate.Enabled(features.BufferGangGroups) {
		return
	}
	go wait.Forever(b.activateBlockingPods, 5*time.Second)
	go wait.Forever(b.activateBufferedPods, 5*time.Second)
}

func (b *PodGroupBuffer) activateBlockingPods() {
	toActivatePods := make(map[string]*corev1.Pod)
	defer func() {
		if len(toActivatePods) != 0 {
			b.activatePodsFunc(toActivatePods)
		}
	}()
	b.Lock()
	defer b.Unlock()
	var toActivateGangGroups, toDeleteGangGroups []string
	for gangGroupID, activated := range b.bufferedGangGroups {
		if activated {
			continue
		}
		gangGroup, _ := b.podGroupManager.GetGangGroupInfo(gangGroupID)
		if gangGroup == nil {
			klog.V(5).Info("buffered gang group %s not found, it may be deleted", gangGroupID)
			toDeleteGangGroups = append(toDeleteGangGroups, gangGroupID)
			continue
		}
		blockingPodIDs, validness := gangGroup.GetBlockPodsIfNotValid()
		if !validness {
			toActivate := true
			toActivatePodsOfCurrentGang := make(map[string]*corev1.Pod)
			for _, podID := range blockingPodIDs {
				namespacedAndName := strings.Split(podID, "/")
				toActivatePod, err := b.podLister.Pods(namespacedAndName[0]).Get(namespacedAndName[1])
				if err != nil || toActivatePod == nil {
					toActivate = false
					klog.V(5).Infof("blocking pod %s not found, it may be deleted, gangGroupId: %s", podID, gangGroupID)
					toDeleteGangGroups = append(toDeleteGangGroups, gangGroupID)
					break
				}
				toActivatePodsOfCurrentGang[podID] = toActivatePod
			}
			if toActivate {
				toActivateGangGroups = append(toActivateGangGroups, gangGroupID)
				for podID, pod := range toActivatePodsOfCurrentGang {
					toActivatePods[podID] = pod
				}
			}
		}
	}
	if len(toActivateGangGroups) > 0 || len(toDeleteGangGroups) > 0 {
		klog.Infof("activated buffered gang groups %v, deleted buffered gang groups %v", toActivateGangGroups, toDeleteGangGroups)
	}
	for _, group := range toDeleteGangGroups {
		delete(b.bufferedGangGroups, group)
	}
	for _, group := range toActivateGangGroups {
		b.bufferedGangGroups[group] = true
	}
}

func (b *PodGroupBuffer) activateBufferedPods() {
	toActivatePods := make(map[string]*corev1.Pod)
	defer func() {
		if len(toActivatePods) != 0 {
			b.activatePodsFunc(toActivatePods)
		}
	}()
	b.Lock()
	defer b.Unlock()
	var toActivateGangGroups, toDeleteGangGroups []string
	for gangGroupID := range b.bufferedGangGroups {
		gangGroup, _ := b.podGroupManager.GetGangGroupInfo(gangGroupID)
		if gangGroup == nil {
			klog.V(5).Info("buffered gang group %s not found, it may be deleted", gangGroupID)
			toDeleteGangGroups = append(toDeleteGangGroups, gangGroupID)
			continue
		}
		allPodIDs, validness := gangGroup.GetAllPodsIfValid()
		if validness {
			toActivate := true
			toActivatePodsOfCurrentGang := make(map[string]*corev1.Pod)
			for _, podID := range allPodIDs {
				namespacedAndName := strings.Split(podID, "/")
				toActivatePod, err := b.podLister.Pods(namespacedAndName[0]).Get(namespacedAndName[1])
				if err != nil || toActivatePod == nil {
					toActivate = false
					klog.V(5).Infof("buffered pod %s not found, it may be deleted, gangGroupId: %s", podID, gangGroupID)
					toDeleteGangGroups = append(toDeleteGangGroups, gangGroupID)
					break
				}
				toActivatePodsOfCurrentGang[podID] = toActivatePod
			}
			if toActivate {
				toActivateGangGroups = append(toActivateGangGroups, gangGroupID)
				for podID, pod := range toActivatePodsOfCurrentGang {
					toActivatePods[podID] = pod
				}
			}
		}
	}
	if len(toActivateGangGroups) > 0 || len(toDeleteGangGroups) > 0 {
		klog.Infof("activated buffered gang groups %v, deleted buffered gang groups %v", toActivateGangGroups, toDeleteGangGroups)
	}
	for _, group := range toDeleteGangGroups {
		delete(b.bufferedGangGroups, group)
	}
	for _, group := range toActivateGangGroups {
		delete(b.bufferedGangGroups, group)
	}
}

func (b *PodGroupBuffer) GetBufferedGangGroups() map[string]bool {
	b.RLock()
	defer b.RUnlock()
	bufferedGangGroups := map[string]bool{}
	for k, v := range b.bufferedGangGroups {
		bufferedGangGroups[k] = v
	}
	return bufferedGangGroups
}
