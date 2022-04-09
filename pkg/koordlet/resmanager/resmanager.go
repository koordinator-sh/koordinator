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

package resmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/component-base/featuregate"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	slolisterv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type ResManager interface {
	Run(stopCh <-chan struct{}) error
}

type resManager struct {
	config                        *Config
	collectResUsedIntervalSeconds int64
	nodeName                      string
	schema                        *apiruntime.Scheme
	statesInformer                statesinformer.StatesInformer
	metricCache                   metriccache.MetricCache
	nodeSLOInformer               cache.SharedIndexInformer
	nodeSLOLister                 slolisterv1alpha1.NodeSLOLister
	kubeClient                    clientset.Interface
	eventRecorder                 record.EventRecorder

	// nodeSLO stores the latest nodeSLO object for the current node
	nodeSLO        *slov1alpha1.NodeSLO
	nodeSLORWMutex sync.RWMutex
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

// mergeSLOSpecResourceUsedThresholdWithBE merges the nodeSLO ResourceUsedThresholdWithBE with default configs
func mergeSLOSpecResourceUsedThresholdWithBE(defaultSpec, newSpec *slov1alpha1.ResourceThresholdStrategy) *slov1alpha1.ResourceThresholdStrategy {
	spec := &slov1alpha1.ResourceThresholdStrategy{}
	if newSpec != nil {
		spec = newSpec
	}
	// ignore err for serializing/deserializing the same struct type
	data, _ := json.Marshal(spec)
	// NOTE: use deepcopy to avoid a overwrite to the global default
	out := defaultSpec.DeepCopy()
	_ = json.Unmarshal(data, &out)
	return out
}

// mergeDefaultNodeSLO merges nodeSLO with default config; ensure use the function with a RWMutex
func (r *resManager) mergeDefaultNodeSLO(nodeSLO *slov1alpha1.NodeSLO) {
	if r.nodeSLO == nil || nodeSLO == nil {
		klog.Errorf("failed to merge with nil nodeSLO, old: %v, new: %v", r.nodeSLO, nodeSLO)
		return
	}

	// merge ResourceUsedThresholdWithBE individually for nil-ResourceUsedThresholdWithBE case
	mergedResourceUsedThresholdWithBESpec := mergeSLOSpecResourceUsedThresholdWithBE(util.DefaultNodeSLOSpecConfig().ResourceUsedThresholdWithBE,
		nodeSLO.Spec.ResourceUsedThresholdWithBE)
	if mergedResourceUsedThresholdWithBESpec != nil {
		r.nodeSLO.Spec.ResourceUsedThresholdWithBE = mergedResourceUsedThresholdWithBESpec
	}
}

func (r *resManager) createNodeSLO(nodeSLO *slov1alpha1.NodeSLO) {
	r.nodeSLORWMutex.Lock()
	defer r.nodeSLORWMutex.Unlock()

	oldNodeSLOStr := util.DumpJSON(r.nodeSLO)

	r.nodeSLO = nodeSLO.DeepCopy()
	r.nodeSLO.Spec = nodeSLO.Spec

	// merge nodeSLO spec with the default config
	r.mergeDefaultNodeSLO(nodeSLO)

	newNodeSLOStr := util.DumpJSON(r.nodeSLO)
	klog.Infof("update nodeSLO content: old %s, new %s", oldNodeSLOStr, newNodeSLOStr)
}

func (r *resManager) getNodeSLOCopy() *slov1alpha1.NodeSLO {
	r.nodeSLORWMutex.Lock()
	defer r.nodeSLORWMutex.Unlock()

	if r.nodeSLO == nil {
		return nil
	}
	nodeSLOCopy := r.nodeSLO.DeepCopy()
	return nodeSLOCopy
}

func (r *resManager) updateNodeSLOSpec(nodeSLO *slov1alpha1.NodeSLO) {
	r.nodeSLORWMutex.Lock()
	defer r.nodeSLORWMutex.Unlock()

	oldNodeSLOStr := util.DumpJSON(r.nodeSLO)

	r.nodeSLO.Spec = nodeSLO.Spec

	// merge nodeSLO spec with the default config
	r.mergeDefaultNodeSLO(nodeSLO)

	newNodeSLOStr := util.DumpJSON(r.nodeSLO)
	klog.Infof("update nodeSLO content: old %s, new %s", oldNodeSLOStr, newNodeSLOStr)
}

func NewResManager(cfg *Config, schema *apiruntime.Scheme, kubeClient clientset.Interface, crdClient *koordclientset.Clientset, nodeName string,
	statesInformer statesinformer.StatesInformer, metricCache metriccache.MetricCache, collectResUsedIntervalSeconds int64) ResManager {
	informer := newNodeSLOInformer(crdClient, nodeName)

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&clientcorev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(schema, corev1.EventSource{Component: "slo-agent-reporter", Host: nodeName})

	r := &resManager{
		config:                        cfg,
		nodeName:                      nodeName,
		schema:                        schema,
		statesInformer:                statesInformer,
		metricCache:                   metricCache,
		nodeSLOInformer:               informer,
		nodeSLOLister:                 slolisterv1alpha1.NewNodeSLOLister(informer.GetIndexer()),
		kubeClient:                    kubeClient,
		eventRecorder:                 recorder,
		collectResUsedIntervalSeconds: collectResUsedIntervalSeconds,
	}
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			nodeSLO, ok := obj.(*slov1alpha1.NodeSLO)
			if ok {
				r.createNodeSLO(nodeSLO)
				klog.Infof("create NodeSLO %v", nodeSLO)
			} else {
				klog.Errorf("node slo informer add func parse nodeSLO failed")
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNodeSLO, oldOK := oldObj.(*slov1alpha1.NodeSLO)
			newNodeSLO, newOK := newObj.(*slov1alpha1.NodeSLO)
			if !oldOK || !newOK {
				klog.Errorf("unable to convert object to *slov1alpha1.NodeSLO, old %T, new %T", oldObj, newObj)
				return
			}
			if reflect.DeepEqual(oldNodeSLO.Spec, newNodeSLO.Spec) {
				klog.V(5).Infof("find NodeSLO spec %s has not changed", newNodeSLO.Name)
				return
			}
			klog.Infof("update NodeSLO spec %v", newNodeSLO.Spec)
			r.updateNodeSLOSpec(newNodeSLO)
		},
	})

	return r
}

// isFeatureDisabled returns whether the featuregate is disabled by nodeSLO config
func isFeatureDisabled(nodeSLO *slov1alpha1.NodeSLO, feature featuregate.Feature) (bool, error) {
	if nodeSLO == nil || nodeSLO.Spec == (slov1alpha1.NodeSLOSpec{}) {
		return false, fmt.Errorf("cannot parse feature config for invalid nodeSLO %v", nodeSLO)
	}

	spec := nodeSLO.Spec
	switch feature {
	case features.BECPUSuppress:
		// nil value means enabled
		if spec.ResourceUsedThresholdWithBE == nil {
			return false, fmt.Errorf("cannot parse feature config for invalid nodeSLO %v", nodeSLO)
		}
		return spec.ResourceUsedThresholdWithBE.Enable != nil && !(*spec.ResourceUsedThresholdWithBE.Enable), nil
	default:
		return false, fmt.Errorf("cannot parse feature config for unsupported feature %s", feature)
	}
}

func (r *resManager) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	klog.Info("Starting resManager")

	klog.Infof("starting informer for NodeSLO")
	go r.nodeSLOInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, r.nodeSLOInformer.HasSynced) {
		return fmt.Errorf("time out waiting for node slo caches to sync")
	}

	if !cache.WaitForCacheSync(stopCh, r.statesInformer.HasSynced) {
		return fmt.Errorf("time out waiting for kubelet meta service caches to sync")
	}
	if !cache.WaitForCacheSync(stopCh, r.hasSynced) {
		return fmt.Errorf("time out waiting for sync NodeSLO")
	}

	cpuSuppress := NewCPUSuppress(r)
	koordletutil.RunFeature(cpuSuppress.suppressBECPU, []featuregate.Feature{features.BECPUSuppress}, r.config.CPUSuppressIntervalSeconds, stopCh)

	klog.Info("Starting resManager successfully")
	<-stopCh
	klog.Info("shutting down resManager")
	return nil
}

func (r *resManager) hasSynced() bool {
	r.nodeSLORWMutex.Lock()
	defer r.nodeSLORWMutex.Unlock()

	return r.nodeSLO != nil && r.nodeSLO.Spec.ResourceUsedThresholdWithBE != nil
}
