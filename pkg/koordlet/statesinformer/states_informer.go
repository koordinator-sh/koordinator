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
	"sync"
	"time"

	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	topologyclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned"
	_ "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/scheme"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/pleg"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	HTTPScheme  = "http"
	HTTPSScheme = "https"
)

type StatesInformer interface {
	Run(stopCh <-chan struct{}) error
	HasSynced() bool

	GetNode() *corev1.Node
	GetNodeSLO() *slov1alpha1.NodeSLO

	GetAllPods() []*PodMeta

	RegisterCallbacks(objType RegisterType, name, description string, callbackFn UpdateCbFn)
}

type statesInformer struct {
	config       *Config
	kubelet      KubeletStub
	podHasSynced *atomic.Bool
	// use pleg to accelerate the efficiency of Pod meta update
	pleg       pleg.Pleg
	podCreated chan string

	nodeInformer cache.SharedIndexInformer
	nodeRWMutex  sync.RWMutex
	node         *corev1.Node

	nodeSLOInformer cache.SharedIndexInformer
	nodeSLORWMutex  sync.RWMutex
	nodeSLO         *slov1alpha1.NodeSLO

	nodeTopoMutex  sync.RWMutex
	nodeTopology   *topov1alpha1.NodeResourceTopology
	topologyClient topologyclientset.Interface

	podRWMutex     sync.RWMutex
	podMap         map[string]*PodMeta
	podUpdatedTime time.Time
	metricsCache   metriccache.MetricCache

	callbackChans        map[RegisterType]chan UpdateCbCtx
	stateUpdateCallbacks map[RegisterType][]updateCallback
}

func NewStatesInformer(config *Config, kubeClient clientset.Interface, crdClient koordclientset.Interface, topologyClient *topologyclientset.Clientset, metricsCache metriccache.MetricCache, pleg pleg.Pleg, nodeName string) StatesInformer {
	nodeInformer := newNodeInformer(kubeClient, nodeName)
	nodeSLOInformer := newNodeSLOInformer(crdClient, nodeName)

	return &statesInformer{
		config:       config,
		podHasSynced: atomic.NewBool(false),

		pleg: pleg,

		nodeInformer:    nodeInformer,
		nodeSLOInformer: nodeSLOInformer,

		podMap:     map[string]*PodMeta{},
		podCreated: make(chan string, 1), // set 1 buffer

		callbackChans: map[RegisterType]chan UpdateCbCtx{
			RegisterTypeNodeSLOSpec:  make(chan UpdateCbCtx, 1),
			RegisterTypeAllPods:      make(chan UpdateCbCtx, 1),
			RegisterTypeNodeTopology: make(chan UpdateCbCtx, 1),
		},
		stateUpdateCallbacks: map[RegisterType][]updateCallback{
			RegisterTypeNodeSLOSpec:  {},
			RegisterTypeAllPods:      {},
			RegisterTypeNodeTopology: {},
		},
		topologyClient: topologyClient,
		metricsCache:   metricsCache,
	}
}

func (s *statesInformer) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	klog.Infof("setup statesInformer")
	s.setupInformers()
	klog.Infof("starting informers")
	go s.nodeInformer.Run(stopCh)
	go s.nodeSLOInformer.Run(stopCh)

	// waiting for node synced.
	waitInformersSynced := []cache.InformerSynced{
		s.nodeInformer.HasSynced, s.nodeSLOInformer.HasSynced}
	if !cache.WaitForCacheSync(stopCh, waitInformersSynced...) {
		return fmt.Errorf("timed out waiting for states informer caches to sync")
	}

	stub, err := newKubeletStubFromConfig(s.GetNode(), s.config)
	if err != nil {
		klog.ErrorS(err, "create kubelet stub")
		return err
	}
	s.kubelet = stub

	if s.config.KubeletSyncInterval > 0 {
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
	} else {
		klog.Fatalf("KubeletSyncIntervalSeconds is %d, statesInformer sync of kubelet is disabled",
			s.config.KubeletSyncInterval)
	}

	// waiting for pods synced.
	waitPodSynced := []cache.InformerSynced{s.podHasSynced.Load}
	if !cache.WaitForCacheSync(stopCh, waitPodSynced...) {
		return fmt.Errorf("timed out waiting for pod caches to sync")
	}

	go s.startCallbackRunners(stopCh)

	go wait.Until(s.reportNodeTopology, s.config.NodeTopologySyncInterval, stopCh)

	klog.Infof("start states informer successfully")
	<-stopCh
	klog.Infof("shutting down states informer daemon")
	return nil
}

func (s *statesInformer) HasSynced() bool {
	return s.podHasSynced.Load() && s.nodeSLOInformer.HasSynced() && s.nodeInformer.HasSynced()
}

func (s *statesInformer) GetNode() *corev1.Node {
	s.nodeRWMutex.RLock()
	defer s.nodeRWMutex.RUnlock()
	if s.node == nil {
		return nil
	}
	return s.node.DeepCopy()
}

func (s *statesInformer) GetAllPods() []*PodMeta {
	s.podRWMutex.RLock()
	defer s.podRWMutex.RUnlock()
	pods := make([]*PodMeta, 0, len(s.podMap))
	for _, pod := range s.podMap {
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
			ListFunc: func(options metav1.ListOptions) (apiruntime.Object, error) {
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

func newNodeSLOInformer(client koordclientset.Interface, nodeName string) cache.SharedIndexInformer {
	tweakListOptionFunc := func(opt *metav1.ListOptions) {
		opt.FieldSelector = "metadata.name=" + nodeName
	}
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (apiruntime.Object, error) {
				tweakListOptionFunc(&options)
				return client.SloV1alpha1().NodeSLOs().List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				tweakListOptionFunc(&options)
				return client.SloV1alpha1().NodeSLOs().Watch(context.TODO(), options)
			},
		},
		&slov1alpha1.NodeSLO{},
		time.Hour*12,
		cache.Indexers{},
	)
}

func (s *statesInformer) setupInformers() {
	s.setupNodeInformer()
	s.setupNodeSLOInformer()
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
