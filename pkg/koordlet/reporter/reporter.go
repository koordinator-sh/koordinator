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

package reporter

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	clientsetbeta1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	clientbeta1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/slo/v1alpha1"
	listerbeta1 "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	// defaultAggregateDurationSeconds is the default metric aggregate duration by seconds
	defaultAggregateDurationSeconds = 60
)

var (
	scheme = runtime.NewScheme()
)

type Reporter interface {
	Run(stopCh <-chan struct{}) error
}

type reporter struct {
	config             *Config
	nodeName           string
	nodeMetricInformer cache.SharedIndexInformer
	nodeMetricLister   listerbeta1.NodeMetricLister
	eventRecorder      record.EventRecorder
	statusUpdater      *statusUpdater

	statesInformer statesinformer.StatesInformer
	metricCache    metriccache.MetricCache

	rwMutex    sync.RWMutex
	nodeMetric *slov1alpha1.NodeMetric
}

func NewReporter(cfg *Config, kubeClient *clientset.Clientset, crdClient *clientsetbeta1.Clientset,
	nodeName string, metricCache metriccache.MetricCache, statesInformer statesinformer.StatesInformer) Reporter {

	informer := newNodeMetricInformer(crdClient, nodeName)

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&clientcorev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})

	recorder := eventBroadcaster.NewRecorder(scheme, corev1.EventSource{Component: "koordlet-reporter", Host: nodeName})

	r := &reporter{
		config:             cfg,
		nodeName:           nodeName,
		nodeMetricInformer: informer,
		nodeMetricLister:   listerbeta1.NewNodeMetricLister(informer.GetIndexer()),
		eventRecorder:      recorder,
		statusUpdater:      newStatusUpdater(crdClient.SloV1alpha1().NodeMetrics()),
		statesInformer:     statesInformer,
		metricCache:        metricCache,
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			nodeMetric, ok := obj.(*slov1alpha1.NodeMetric)
			if ok {
				r.createNodeMetric(nodeMetric)
			} else {
				klog.Errorf("node metric informer add func parse nodeMetric failed")
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNodeMetric, oldOK := oldObj.(*slov1alpha1.NodeMetric)
			newNodeMetric, newOK := newObj.(*slov1alpha1.NodeMetric)
			if !oldOK || !newOK {
				klog.Errorf("unable to convert object to *slov1alpha1.NodeMetric, old %T, new %T", oldObj, newObj)
				return
			}
			if reflect.DeepEqual(oldNodeMetric.Spec, newNodeMetric.Spec) {
				klog.V(5).Infof("find nodeMetric spec %s has not changed.", newNodeMetric.Name)
				return
			}
			klog.Infof("update node metric spec %v", newNodeMetric.Spec)
			r.updateMetricSpec(newNodeMetric)
		},
	})

	return r
}

func (r *reporter) ReportEvent(object runtime.Object, eventType, reason, message string) {
	r.eventRecorder.Eventf(object, eventType, reason, message)
}

func (r *reporter) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	klog.Infof("starting reporter")

	if r.config.ReportIntervalSeconds > 0 {
		klog.Info("starting informer for NodeMetric")
		go r.nodeMetricInformer.Run(stopCh)
		if !cache.WaitForCacheSync(stopCh, r.nodeMetricInformer.HasSynced, r.statesInformer.HasSynced) {
			return fmt.Errorf("timed out waiting for node metric caches to sync")
		}

		go r.syncNodeMetricWorker(stopCh)

	} else {
		klog.Infof("ReportIntervalSeconds is %d, sync node metric to apiserver is disabled",
			r.config.ReportIntervalSeconds)
	}

	klog.Info("start reporter successfully")
	<-stopCh
	klog.Info("shutting down reporter daemon")
	return nil
}

func (r *reporter) syncNodeMetricWorker(stopCh <-chan struct{}) {
	reportInterval := r.getNodeMetricReportInterval()
	for {
		select {
		case <-stopCh:
			return
		case <-time.After(reportInterval):
			r.sync()
			reportInterval = r.getNodeMetricReportInterval()
		}
	}
}

func (r *reporter) getNodeMetricReportInterval() time.Duration {
	reportInterval := time.Duration(r.config.ReportIntervalSeconds) * time.Second
	nodeMetric, err := r.nodeMetricLister.Get(r.nodeName)
	if err == nil &&
		nodeMetric.Spec.CollectPolicy != nil &&
		nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds != nil {
		interval := *nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds
		if interval > 0 {
			reportInterval = time.Duration(interval) * time.Second
		}
	}
	return reportInterval
}

func (r *reporter) sync() {
	if !r.isNodeMetricInited() {
		klog.Warningf("node metric has not initialized, skip this round.")
		return
	}

	nodeMetricInfo, podMetricInfo := r.collectMetric()
	if nodeMetricInfo == nil {
		klog.Warningf("node metric is not ready, skip this round.")
		return
	}

	newStatus := &slov1alpha1.NodeMetricStatus{
		UpdateTime: &metav1.Time{Time: time.Now()},
		NodeMetric: nodeMetricInfo,
		PodsMetric: podMetricInfo,
	}
	retErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		nodeMetric, err := r.nodeMetricLister.Get(r.nodeName)
		if errors.IsNotFound(err) {
			klog.Warningf("nodeMetric %v not found, skip", r.nodeName)
			return nil
		} else if err != nil {
			klog.Warningf("failed to get %s nodeMetric: %v", r.nodeName, err)
			return err
		}
		err = r.statusUpdater.updateStatus(nodeMetric, newStatus)
		return err
	})

	if retErr != nil {
		klog.Warningf("update node metric status failed, status %v, err %v", util.DumpJSON(newStatus), retErr)
	} else {
		klog.V(4).Infof("update node metric status success, detail: %v", util.DumpJSON(newStatus))
	}
}

func newNodeMetricInformer(client clientsetbeta1.Interface, nodeName string) cache.SharedIndexInformer {
	tweakListOptionsFunc := func(opt *metav1.ListOptions) {
		opt.FieldSelector = "metadata.name=" + nodeName
	}

	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				tweakListOptionsFunc(&options)
				return client.SloV1alpha1().NodeMetrics().List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				tweakListOptionsFunc(&options)
				return client.SloV1alpha1().NodeMetrics().Watch(context.TODO(), options)
			},
		},
		&slov1alpha1.NodeMetric{},
		time.Hour*12,
		cache.Indexers{},
	)
}

func (r *reporter) isNodeMetricInited() bool {
	r.rwMutex.RLock()
	defer r.rwMutex.RUnlock()
	return r.nodeMetric != nil
}

func (r *reporter) createNodeMetric(nodeMetric *slov1alpha1.NodeMetric) {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()
	r.nodeMetric = nodeMetric
}

func (r *reporter) updateMetricSpec(nodeMetric *slov1alpha1.NodeMetric) {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()
	r.nodeMetric.Spec = nodeMetric.Spec
}

// generateQueryParams generate query params. It assumes the nodeMetric is initialized
func (r *reporter) generateQueryParams() *metriccache.QueryParam {
	aggregateDurationSeconds := defaultAggregateDurationSeconds

	end := time.Now()
	start := end.Add(-time.Duration(aggregateDurationSeconds) * time.Second)
	queryParam := &metriccache.QueryParam{
		Aggregate: metriccache.AggregationTypeAVG,
		Start:     &start,
		End:       &end,
	}
	return queryParam
}

func (r *reporter) collectMetric() (*slov1alpha1.NodeMetricInfo, []*slov1alpha1.PodMetricInfo) {
	// collect node's and all pods' metrics with the same query param
	queryParam := r.generateQueryParams()
	nodeMetricInfo := r.collectNodeMetric(queryParam)
	podsMeta := r.statesInformer.GetAllPods()
	podsMetricInfo := make([]*slov1alpha1.PodMetricInfo, 0, len(podsMeta))
	for _, podMeta := range podsMeta {
		podMetric := r.collectPodMetric(podMeta, queryParam)
		if podMetric != nil {
			podsMetricInfo = append(podsMetricInfo, podMetric)
		}
	}
	return nodeMetricInfo, podsMetricInfo
}

func (r *reporter) collectNodeMetric(queryParam *metriccache.QueryParam) *slov1alpha1.NodeMetricInfo {
	queryResult := r.metricCache.GetNodeResourceMetric(queryParam)
	if queryResult.Error != nil {
		klog.Warningf("get node resource metric failed, error %v", queryResult.Error)
		return nil
	}
	if queryResult.Metric == nil {
		klog.Warningf("node metric not exist")
		return nil
	}
	return &slov1alpha1.NodeMetricInfo{
		NodeUsage: *convertNodeMetricToResourceMap(queryResult.Metric),
	}
}

func (r *reporter) collectPodMetric(podMeta *statesinformer.PodMeta, queryParam *metriccache.QueryParam) *slov1alpha1.PodMetricInfo {
	if podMeta == nil || podMeta.Pod == nil {
		return nil
	}
	podUID := string(podMeta.Pod.UID)
	queryResult := r.metricCache.GetPodResourceMetric(&podUID, queryParam)
	if queryResult.Error != nil {
		klog.Warningf("get pod %v resource metric failed, error %v", podUID, queryResult.Error)
		return nil
	}
	if queryResult.Metric == nil {
		klog.Warningf("pod %v metric not exist", podUID)
		return nil
	}
	return &slov1alpha1.PodMetricInfo{
		Namespace: podMeta.Pod.Namespace,
		Name:      podMeta.Pod.Name,
		PodUsage:  *convertPodMetricToResourceMap(queryResult.Metric),
	}
}

const (
	statusUpdateQPS   = 0.1
	statusUpdateBurst = 2
)

type statusUpdater struct {
	nodeMetricClient  clientbeta1.NodeMetricInterface
	previousTimestamp time.Time
	rateLimiter       *rate.Limiter
}

func newStatusUpdater(nodeMetricClient clientbeta1.NodeMetricInterface) *statusUpdater {
	return &statusUpdater{
		nodeMetricClient:  nodeMetricClient,
		previousTimestamp: time.Now().Add(-time.Hour * 24),
		rateLimiter:       rate.NewLimiter(statusUpdateQPS, statusUpdateBurst),
	}
}

func (su *statusUpdater) updateStatus(nodeMetric *slov1alpha1.NodeMetric, newStatus *slov1alpha1.NodeMetricStatus) error {
	if !su.rateLimiter.Allow() {
		msg := fmt.Sprintf("Updating status is limited qps=%v burst=%v", statusUpdateQPS, statusUpdateBurst)
		return fmt.Errorf(msg)
	}

	newNodeMetric := nodeMetric.DeepCopy()
	newNodeMetric.Status = *newStatus

	_, err := su.nodeMetricClient.UpdateStatus(context.TODO(), newNodeMetric, metav1.UpdateOptions{})
	su.previousTimestamp = time.Now()
	return err
}

func convertNodeMetricToResourceMap(nodeMetric *metriccache.NodeResourceMetric) *slov1alpha1.ResourceMap {
	return &slov1alpha1.ResourceMap{
		ResourceList: corev1.ResourceList{
			corev1.ResourceCPU:    nodeMetric.CPUUsed.CPUUsed,
			corev1.ResourceMemory: nodeMetric.MemoryUsed.MemoryWithoutCache,
		},
	}
}

func convertPodMetricToResourceMap(podMetric *metriccache.PodResourceMetric) *slov1alpha1.ResourceMap {
	return &slov1alpha1.ResourceMap{
		ResourceList: corev1.ResourceList{
			corev1.ResourceCPU:    podMetric.CPUUsed.CPUUsed,
			corev1.ResourceMemory: podMetric.MemoryUsed.MemoryWithoutCache,
		},
	}
}
