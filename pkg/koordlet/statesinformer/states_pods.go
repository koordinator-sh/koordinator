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
	"sync"
	"time"

	"go.uber.org/atomic"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/pleg"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

const (
	podsInformerName pluginName = "podsInformer"
)

type podsInformer struct {
	config *Config

	podRWMutex     sync.RWMutex
	podMap         map[string]*PodMeta
	podUpdatedTime time.Time
	podHasSynced   *atomic.Bool

	// use pleg to accelerate the efficiency of Pod meta update
	pleg       pleg.Pleg
	podCreated chan string

	kubelet      KubeletStub
	nodeInformer *nodeInformer

	callbackRunner *callbackRunner
}

func NewPodsInformer() *podsInformer {
	p, err := pleg.NewPLEG(system.Conf.CgroupRootDir)
	if err != nil {
		klog.Fatalf("failed to create PLEG, %v", err)
	}

	podsInformer := &podsInformer{
		podMap:       map[string]*PodMeta{},
		podHasSynced: atomic.NewBool(false),
		pleg:         p,
	}
	return podsInformer
}

func (s *podsInformer) Setup(ctx *pluginOption, states *pluginState) {
	s.config = ctx.config

	nodeInformerIf := states.informerPlugins[nodeInformerName]
	nodeInformer, ok := nodeInformerIf.(*nodeInformer)
	if !ok {
		klog.Fatalf("node informer format error")
	}
	s.nodeInformer = nodeInformer

	s.callbackRunner = states.callbackRunner
}

func (s *podsInformer) Start(stopCh <-chan struct{}) {
	klog.V(2).Infof("starting pod informer")
	if !cache.WaitForCacheSync(stopCh, s.nodeInformer.HasSynced) {
		klog.Fatalf("timed out waiting for pod caches to sync")
	}
	if s.config.KubeletSyncInterval <= 0 {
		return
	}
	stub, err := newKubeletStubFromConfig(s.nodeInformer.GetNode(), s.config)
	if err != nil {
		klog.Fatalf("create kubelet stub, %v", err)
	}
	s.kubelet = stub
	hdlID := s.pleg.AddHandler(pleg.PodLifeCycleHandlerFuncs{
		PodAddedFunc: func(podID string) {
			// There is no need to notify to update the data when the channel is not empty
			if len(s.podCreated) == 0 {
				s.podCreated <- podID
			}
		},
	})
	defer s.pleg.RemoverHandler(hdlID)

	go s.syncKubeletLoop(s.config.KubeletSyncInterval, stopCh)
	go func() {
		if err := s.pleg.Run(stopCh); err != nil {
			klog.Fatalf("Unable to run the pleg: ", err)
		}
	}()
	klog.V(2).Infof("pod informer started")
}

func (s *podsInformer) HasSynced() bool {
	synced := s.podHasSynced.Load()
	klog.V(5).Infof("pods informer has synced %v", synced)
	return synced
}

func (s *podsInformer) GetAllPods() []*PodMeta {
	s.podRWMutex.RLock()
	defer s.podRWMutex.RUnlock()
	pods := make([]*PodMeta, 0, len(s.podMap))
	for _, pod := range s.podMap {
		pods = append(pods, pod.DeepCopy())
	}
	return pods
}

func (s *podsInformer) syncPods() error {
	podList, err := s.kubelet.GetAllPods()

	// when kubelet recovers from crash, podList may be empty.
	if err != nil || len(podList.Items) == 0 {
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
	s.podMap = newPodMap
	s.podHasSynced.Store(true)
	s.podUpdatedTime = time.Now()
	klog.Infof("get pods success, len %d", len(s.podMap))
	s.callbackRunner.SendCallback(RegisterTypeAllPods)
	return nil
}

func (s *podsInformer) syncKubeletLoop(duration time.Duration, stopCh <-chan struct{}) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	s.syncPods()
	// TODO add a config to setup the values
	rateLimiter := rate.NewLimiter(5, 10)
	for {
		select {
		case <-s.podCreated:
			if rateLimiter.Allow() {
				// sync kubelet triggered immediately when the Pod is created
				s.syncPods()
				// reset timer to
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(duration)
			}
		case <-timer.C:
			timer.Reset(duration)
			s.syncPods()
		case <-stopCh:
			klog.Infof("sync kubelet loop is exited")
			return
		}
	}
}

func newKubeletStubFromConfig(node *corev1.Node, cfg *Config) (KubeletStub, error) {
	var address string
	var err error
	var port int
	var scheme string
	var restConfig *rest.Config

	addressPreferredType := corev1.NodeAddressType(cfg.KubeletPreferredAddressType)
	// if the address of the specified type has not been set or error type, InternalIP will be used.
	if !util.IsNodeAddressTypeSupported(addressPreferredType) {
		klog.Warningf("Wrong address type or empty type, InternalIP will be used, error: (%+v).", addressPreferredType)
		addressPreferredType = corev1.NodeInternalIP
	}
	address, err = util.GetNodeAddress(node, addressPreferredType)
	if err != nil {
		klog.Fatalf("Get node address error: %v type(%s) ", err, cfg.KubeletPreferredAddressType)
		return nil, err
	}

	if cfg.InsecureKubeletTLS {
		port = int(cfg.KubeletReadOnlyPort)
		scheme = HTTPScheme
	} else {
		restConfig, err = config.GetConfig()
		if err != nil {
			return nil, err
		}
		restConfig.TLSClientConfig.Insecure = true
		restConfig.TLSClientConfig.CAData = nil
		restConfig.TLSClientConfig.CAFile = ""
		port = int(node.Status.DaemonEndpoints.KubeletEndpoint.Port)
		scheme = HTTPSScheme
	}

	return NewKubeletStub(address, port, scheme, cfg.KubeletSyncTimeout, restConfig)
}

func genPodCgroupParentDir(pod *corev1.Pod) string {
	// todo use cri interface to get pod cgroup dir
	// e.g. kubepods-burstable.slice/kubepods-burstable-pod9dba1d9e_67ba_4db6_8a73_fb3ea297c363.slice/
	return util.GetPodKubeRelativePath(pod)
}
