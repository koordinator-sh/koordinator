/*
 Copyright 2022 The Koordinator Authors.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package statesinformer

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"go.uber.org/atomic"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/pleg"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type StatesInformer interface {
	Run(stopCh <-chan struct{}) error
	HasSynced() bool

	GetNode() *corev1.Node

	GetAllPods() []*PodMeta
}

type statesInformer struct {
	config    *Config
	kubelet   KubeletStub
	hasSynced *atomic.Bool
	// use pleg to accelerate the efficiency of Pod meta update
	pleg       pleg.Pleg
	podCreated chan string

	nodeInformer cache.SharedIndexInformer
	nodeRWMutex  sync.RWMutex
	node         *corev1.Node

	podRWMutex     sync.RWMutex
	podMap         map[string]*PodMeta
	podUpdatedTime time.Time
}

func NewStatesInformer(config *Config, kubeClient clientset.Interface, pleg pleg.Pleg, nodeName string) StatesInformer {
	nodeInformer := newNodeInformer(kubeClient, nodeName)

	m := &statesInformer{
		config:    config,
		kubelet:   NewKubeletStub(config.KubeletIPAddr, config.KubeletHTTPPort, config.KubeletSyncTimeoutSeconds),
		hasSynced: atomic.NewBool(false),

		pleg: pleg,

		nodeInformer: nodeInformer,

		podMap:     map[string]*PodMeta{},
		podCreated: make(chan string, 1), // set 1 buffer
	}
	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			node, ok := obj.(*corev1.Node)
			if ok {
				m.syncNode(node)
			} else {
				klog.Errorf("node informer add func parse Node failed, obj %T", obj)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNode, oldOK := oldObj.(*corev1.Node)
			newNode, newOK := newObj.(*corev1.Node)
			if !oldOK || !newOK {
				klog.Errorf("unable to convert object to *corev1.Node, old %T, new %T", oldObj, newObj)
				return
			}
			if reflect.DeepEqual(oldNode, newNode) {
				klog.V(5).Infof("find node %s has not changed", newNode.Name)
				return
			}
			m.syncNode(newNode)
		},
	})
	return m
}

func (m *statesInformer) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	klog.Infof("starting statesInformer")

	klog.Infof("starting informer for Node")
	go m.nodeInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, m.nodeInformer.HasSynced) {
		return fmt.Errorf("timed out waiting for node caches to sync")
	}

	if m.config.KubeletSyncIntervalSeconds > 0 {
		hdlID := m.pleg.AddHandler(pleg.PodLifeCycleHandlerFuncs{
			PodAddedFunc: func(podID string) {
				// There is no need to notify to update the data when the channel is not empty
				if len(m.podCreated) == 0 {
					m.podCreated <- podID
				}
			},
		})
		defer m.pleg.RemoverHandler(hdlID)

		go m.syncKubeletLoop(time.Duration(m.config.KubeletSyncIntervalSeconds)*time.Second, stopCh)
	} else {
		klog.Infof("KubeletSyncIntervalSeconds is %d, statesInformer sync of kubelet is disabled",
			m.config.KubeletSyncIntervalSeconds)
	}
	klog.Infof("start meta service successfully")
	<-stopCh
	klog.Infof("shutting down meta service daemon")
	return nil
}

func (m *statesInformer) HasSynced() bool {
	return m.hasSynced.Load()
}

func (m *statesInformer) GetNode() *corev1.Node {
	m.nodeRWMutex.RLock()
	defer m.nodeRWMutex.RUnlock()
	if m.node == nil {
		return nil
	}
	return m.node.DeepCopy()
}

func (m *statesInformer) GetAllPods() []*PodMeta {
	m.podRWMutex.RLock()
	defer m.podRWMutex.RUnlock()
	pods := make([]*PodMeta, 0, len(m.podMap))
	for _, pod := range m.podMap {
		pods = append(pods, pod.DeepCopy())
	}
	return pods
}

func newNodeInformer(client clientset.Interface, nodeName string) cache.SharedIndexInformer {
	tweakListOptionsFunc := func(opt *metav1.ListOptions) {
		opt.FieldSelector = "metadata.name=" + nodeName
	}

	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				tweakListOptionsFunc(&options)
				return client.CoreV1().Nodes().List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				tweakListOptionsFunc(&options)
				return client.CoreV1().Nodes().Watch(context.TODO(), options)
			},
		},
		&corev1.Node{},
		time.Hour*12,
		cache.Indexers{},
	)
}

func (m *statesInformer) syncNode(newNode *corev1.Node) {
	klog.V(5).Infof("node update detail %v", newNode)
	m.nodeRWMutex.Lock()
	defer m.nodeRWMutex.Unlock()
	m.node = newNode

	// also register node for metrics
	metrics.Register(newNode)
}

func (m *statesInformer) syncKubelet() error {
	podList, err := m.kubelet.GetAllPods()
	if err != nil {
		klog.Warningf("get pods from kubelet failed, err: %v", err)
		return err
	}
	newPodMap := make(map[string]*PodMeta, len(podList.Items))
	for _, pod := range podList.Items {
		newPodMap[string(pod.UID)] = &PodMeta{
			Pod:       pod.DeepCopy(),
			CgroupDir: genPodCgroupParentDir(&pod),
		}
	}
	m.podMap = newPodMap
	m.hasSynced.Store(true)
	m.podUpdatedTime = time.Now()
	klog.Infof("get pods from kubelet success, len %d", len(m.podMap))
	return nil
}

func (m *statesInformer) syncKubeletLoop(duration time.Duration, stopCh <-chan struct{}) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	// TODO add a config to setup the values
	rateLimiter := rate.NewLimiter(5, 10)
	for {
		select {
		case <-m.podCreated:
			if rateLimiter.Allow() {
				// sync kubelet triggered immediately when the Pod is created
				m.syncKubelet()
				// reset timer to
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(duration)
			}
		case <-timer.C:
			timer.Reset(duration)
			m.syncKubelet()
		case <-stopCh:
			klog.Infof("sync kubelet loop is exited")
			return
		}
	}
}

func genPodCgroupParentDir(pod *corev1.Pod) string {
	// todo use cri interface to get pod cgroup dir
	// e.g. kubepods-burstable.slice/kubepods-burstable-pod9dba1d9e_67ba_4db6_8a73_fb3ea297c363.slice/
	return util.GetPodKubeRelativePath(pod)
}
