package gang

import (
	"github.com/koordinator-sh/koordinator/apis/extension"
	"k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sync"
)

type gangCache struct {
	lock      *sync.RWMutex
	gangItems map[string]*Gang
}

func NewGangCache() *gangCache {
	return &gangCache{
		gangItems: make(map[string]*Gang),
		lock:      new(sync.RWMutex),
	}
}

/*****************Cache BEG*****************/
func (gangCache *gangCache) createGangToCache(gangId string) *Gang {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	if gang, exist := gangCache.gangItems[gangId]; exist {
		return gang
	}

	gang := NewGang(gangId)
	gangCache.gangItems[gangId] = gang
	return gang
}

func (gangCache *gangCache) GetGangFromCache(gangId string) *Gang {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()

	return gangCache.gangItems[gangId]
}

func (gangCache *gangCache) deleteGangFromCache(gangId string) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	delete(gangCache.gangItems, gangId)
}

/*****************Cache END*****************/

/******************Pod Event BEG******************/
func (gangCache *gangCache) OnPodAdd(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}

	if !IsValidPod(pod) {
		return
	}

	gangNamespace := pod.Annotations[extension.GangNamespaceAnnotation]
	gangName := pod.Annotations[extension.GangNameAnnotation]

	gangId := GetNamespaceSplicingName(gangNamespace, gangName)
	gang := gangCache.GetGangFromCache(gangId)
	if gang == nil {
		gang = gangCache.createGangToCache(gangId)
		klog.Infof("Create gang by pod on add, gangName:%v", gangId)
	}

	if ShouldInitGangByPodConfig(pod) {
		gang.TryInitByPodConfig(pod)
	}
}

func (gangCache *gangCache) OnPodUpdate(oldObj interface{}, newObj interface{}) {
}

func (gangCache *gangCache) OnPodDelete(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}

	if !IsValidPod(pod) {
		return
	}

	gangNamespace := pod.Annotations[extension.GangNamespaceAnnotation]
	gangName := pod.Annotations[extension.GangNameAnnotation]

	gangId := GetNamespaceSplicingName(gangNamespace, gangName)
	gang := gangCache.GetGangFromCache(gangId)
	if gang == nil {
		return
	}

	shouldDeleteGang := gang.deletePod(pod)
	if shouldDeleteGang {
		gangCache.deleteGangFromCache(gangId)
		klog.Infof("Delete gang from gang cache, gangName:%v", gang.Name)
	}
}

/******************Pod Event END******************/

/******************GANG Event BEG******************/
func (gangCache *gangCache) OnGangAdd(obj interface{}) {
	//todo, parse podGroup
}

func (gangCache *gangCache) OnGangUpdate(obj interface{}) {
}

func (gangCache *gangCache) OnGangDelete(obj interface{}) {
	//todo, parse podGroup
}

/******************GANG Event END******************/

func ShouldInitGangByPodConfig(pod *v1.Pod) bool {
	_, exist := pod.Annotations[extension.GangMinNumAnnotation]
	return exist
}

func IsValidPod(pod *v1.Pod) bool {
	if pod.Annotations == nil {
		return false
	}

	if _, exist := pod.Annotations[extension.GangNameAnnotation]; !exist {
		return false
	}

	return true
}
