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

package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	clientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	clientsetv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/slo/v1alpha1"
	listerv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/prediction"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	nodeMetricInformerName PluginName = "nodeMetricInformer"

	// defaultAggregateDurationSeconds is the default metric aggregate duration by seconds
	minAggregateDurationSeconds     = 60
	defaultAggregateDurationSeconds = 300

	defaultReportIntervalSeconds = 60
	minReportIntervalSeconds     = 30

	// metric is valid only if its (lastSample.Time - firstSample.Time) > 0.5 * targetTimeRange
	// used during checking node aggregate usage for cold start
	validateTimeRangeRatio = 0.5
)

var (
	scheme = runtime.NewScheme()

	defaultNodeMetricSpec = slov1alpha1.NodeMetricSpec{
		CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
			AggregateDurationSeconds: pointer.Int64(defaultAggregateDurationSeconds),
			ReportIntervalSeconds:    pointer.Int64(defaultReportIntervalSeconds),
			NodeAggregatePolicy: &slov1alpha1.AggregatePolicy{
				Durations: []metav1.Duration{
					{Duration: 5 * time.Minute},
					{Duration: 10 * time.Minute},
					{Duration: 30 * time.Minute},
				},
			},
		},
	}
)

type nodeMetricInformer struct {
	reportEnabled      bool
	nodeName           string
	nodeMetricInformer cache.SharedIndexInformer
	nodeMetricLister   listerv1alpha1.NodeMetricLister
	eventRecorder      record.EventRecorder
	statusUpdater      *statusUpdater

	podsInformer     *podsInformer
	metricCache      metriccache.MetricCache
	predictorFactory prediction.PredictorFactory

	rwMutex    sync.RWMutex
	nodeMetric *slov1alpha1.NodeMetric
}

func NewNodeMetricInformer() *nodeMetricInformer {
	return &nodeMetricInformer{}
}

func (r *nodeMetricInformer) HasSynced() bool {
	if !r.reportEnabled {
		return true
	}
	if r.nodeMetricInformer == nil {
		return false
	}
	synced := r.nodeMetricInformer.HasSynced()
	klog.V(5).Infof("node metric informer has synced %v", synced)
	return synced
}

func (r *nodeMetricInformer) Setup(ctx *PluginOption, state *PluginState) {
	r.reportEnabled = ctx.config.EnableNodeMetricReport
	r.nodeName = ctx.NodeName
	r.nodeMetricInformer = newNodeMetricInformer(ctx.KoordClient, ctx.NodeName)
	r.nodeMetricLister = listerv1alpha1.NewNodeMetricLister(r.nodeMetricInformer.GetIndexer())

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&clientcorev1.EventSinkImpl{Interface: ctx.KubeClient.CoreV1().Events("")})
	r.eventRecorder = eventBroadcaster.NewRecorder(scheme, corev1.EventSource{Component: "koordlet-NodeMetric", Host: ctx.NodeName})

	r.statusUpdater = newStatusUpdater(ctx.KoordClient.SloV1alpha1().NodeMetrics())

	r.metricCache = state.metricCache
	podsInformerIf := state.informerPlugins[podsInformerName]
	podsInformer, ok := podsInformerIf.(*podsInformer)
	if !ok {
		klog.Fatalf("pods informer format error")
	}
	r.podsInformer = podsInformer
	r.predictorFactory = state.predictorFactory

	r.nodeMetricInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			nodeMetric, ok := obj.(*slov1alpha1.NodeMetric)
			if ok {
				r.updateMetricSpec(nodeMetric)
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

			if newNodeMetric.Generation == oldNodeMetric.Generation || reflect.DeepEqual(oldNodeMetric.Spec, newNodeMetric.Spec) {
				klog.V(5).Infof("find nodeMetric spec %s has not changed.", newNodeMetric.Name)
				return
			}
			klog.V(2).Infof("update node metric spec %v", newNodeMetric.Spec)
			r.updateMetricSpec(newNodeMetric)
		},
	})
}

func (r *nodeMetricInformer) ReportEvent(object runtime.Object, eventType, reason, message string) {
	r.eventRecorder.Eventf(object, eventType, reason, message)
}

func (r *nodeMetricInformer) Start(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	klog.Infof("starting nodeMetricInformer")

	if !r.reportEnabled {
		klog.Infof("node metric report is disabled.")
		return
	}

	go r.nodeMetricInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, r.nodeMetricInformer.HasSynced, r.podsInformer.HasSynced) {
		klog.Errorf("timed out waiting for node metric caches to sync")
	}
	go r.syncNodeMetricWorker(stopCh)

	klog.Info("start nodeMetricInformer successfully")
	<-stopCh
	klog.Info("shutting down nodeMetricInformer daemon")
}

func (r *nodeMetricInformer) syncNodeMetricWorker(stopCh <-chan struct{}) {
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

func (r *nodeMetricInformer) getNodeMetricReportInterval() time.Duration {
	r.rwMutex.RLock()
	defer r.rwMutex.RUnlock()
	if r.nodeMetric == nil || r.nodeMetric.Spec.CollectPolicy == nil || r.nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds == nil {
		return time.Duration(defaultReportIntervalSeconds) * time.Second
	}
	reportIntervalSeconds := util.MaxInt64(*r.nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds, minReportIntervalSeconds)
	return time.Duration(reportIntervalSeconds) * time.Second
}

func (r *nodeMetricInformer) getNodeMetricAggregateDuration() time.Duration {
	r.rwMutex.RLock()
	defer r.rwMutex.RUnlock()
	if r.nodeMetric.Spec.CollectPolicy == nil || r.nodeMetric.Spec.CollectPolicy.AggregateDurationSeconds == nil {
		return time.Duration(defaultAggregateDurationSeconds) * time.Second
	}
	aggregateDurationSeconds := util.MaxInt64(*r.nodeMetric.Spec.CollectPolicy.AggregateDurationSeconds, minAggregateDurationSeconds)
	return time.Duration(aggregateDurationSeconds) * time.Second
}

func (r *nodeMetricInformer) getNodeMetricSpec() *slov1alpha1.NodeMetricSpec {
	r.rwMutex.RLock()
	defer r.rwMutex.RUnlock()
	if r.nodeMetric == nil {
		return &defaultNodeMetricSpec
	}
	return r.nodeMetric.Spec.DeepCopy()
}

func (r *nodeMetricInformer) sync() {
	if !r.isNodeMetricInited() {
		klog.Warningf("node metric has not initialized, skip this round.")
		return
	}

	nodeMetricInfo, podMetricInfo, prodReclaimableMetric := r.collectMetric()
	if nodeMetricInfo == nil {
		klog.Warningf("node metric is not ready, skip this round.")
		return
	}

	newStatus := &slov1alpha1.NodeMetricStatus{
		UpdateTime:            &metav1.Time{Time: time.Now()},
		NodeMetric:            nodeMetricInfo,
		PodsMetric:            podMetricInfo,
		ProdReclaimableMetric: prodReclaimableMetric,
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

func newNodeMetricInformer(client clientset.Interface, nodeName string) cache.SharedIndexInformer {
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

func (r *nodeMetricInformer) isNodeMetricInited() bool {
	r.rwMutex.RLock()
	defer r.rwMutex.RUnlock()
	return r.nodeMetric != nil
}

func (r *nodeMetricInformer) updateMetricSpec(newNodeMetric *slov1alpha1.NodeMetric) {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()
	if newNodeMetric == nil {
		klog.Error("failed to merge with nil nodeMetric, new is nil")
		return
	}
	r.nodeMetric = newNodeMetric.DeepCopy()
	data, _ := json.Marshal(newNodeMetric.Spec)
	r.nodeMetric.Spec = *defaultNodeMetricSpec.DeepCopy()
	_ = json.Unmarshal(data, &r.nodeMetric.Spec)
}

// generateQueryDuration generate query params. It assumes the nodeMetric is initialized
func (r *nodeMetricInformer) generateQueryDuration() (start time.Time, end time.Time) {
	aggregateDuration := r.getNodeMetricAggregateDuration()
	end = time.Now()
	start = end.Add(-aggregateDuration * time.Second)
	return
}

func (r *nodeMetricInformer) collectMetric() (*slov1alpha1.NodeMetricInfo, []*slov1alpha1.PodMetricInfo, *slov1alpha1.ReclaimableMetric) {
	spec := r.getNodeMetricSpec()
	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(*spec.CollectPolicy.AggregateDurationSeconds) * time.Second)

	nodeMetricInfo := &slov1alpha1.NodeMetricInfo{
		NodeUsage:            r.queryNodeMetric(startTime, endTime, metriccache.AggregationTypeAVG, false),
		AggregatedNodeUsages: r.collectNodeAggregateMetric(endTime, spec.CollectPolicy.NodeAggregatePolicy),
	}

	var gpus koordletutil.GPUDevices
	value, ok := r.metricCache.Get(koordletutil.GPUDeviceType)
	if ok {
		gpus, ok = value.(koordletutil.GPUDevices)
		if !ok {
			klog.Errorf("value type error, expect: %T, got %T", koordletutil.GPUDevices{}, value)
		}
	}

	podsMeta := r.podsInformer.GetAllPods()
	podsMetricInfo := make([]*slov1alpha1.PodMetricInfo, 0, len(podsMeta))
	podQueryParam := metriccache.QueryParam{
		Aggregate: metriccache.AggregationTypeAVG,
		Start:     &startTime,
		End:       &endTime,
	}
	prodPredictor := r.predictorFactory.New(prediction.ProdReclaimablePredictor)
	for _, podMeta := range podsMeta {
		podMetric, err := r.collectPodMetric(podMeta, podQueryParam)
		if err != nil {
			klog.Warningf("query pod metric failed, pod %s/%s, error %v", genPodMetaKey(podMeta), err)
			continue
		}
		// predict pods which have valid metrics; ignore prediction failures
		err = prodPredictor.AddPod(podMeta.Pod)
		if err != nil {
			klog.V(4).Infof("predictor add pod aborted, pod %s/%s, error %v", genPodMetaKey(podMeta), err)
		}

		r.fillExtensionMap(podMetric, podMeta.Pod)
		if len(gpus) > 0 {
			r.fillGPUMetrics(podQueryParam, podMetric, string(podMeta.Pod.UID), gpus)
		}
		podsMetricInfo = append(podsMetricInfo, podMetric)
	}
	prodReclaimable := &slov1alpha1.ReclaimableMetric{}
	if p, err := prodPredictor.GetResult(); err != nil {
		klog.Errorf("failed to get prediction, err %v", err)
		metrics.RecordNodeResourcePriorityReclaimable(string(corev1.ResourceCPU), metrics.UnitCore, string(apiext.PriorityProd), 0)
		metrics.RecordNodeResourcePriorityReclaimable(string(corev1.ResourceMemory), metrics.UnitByte, string(apiext.PriorityProd), 0)
	} else {
		prodReclaimable.Resource = slov1alpha1.ResourceMap{ResourceList: p}
		metrics.RecordNodeResourcePriorityReclaimable(string(corev1.ResourceCPU), metrics.UnitCore, string(apiext.PriorityProd), float64(p.Cpu().MilliValue())/1000)
		metrics.RecordNodeResourcePriorityReclaimable(string(corev1.ResourceMemory), metrics.UnitByte, string(apiext.PriorityProd), float64(p.Memory().Value()))
	}

	return nodeMetricInfo, podsMetricInfo, prodReclaimable
}

func (r *nodeMetricInformer) queryNodeMetric(start time.Time, end time.Time, aggregateType metriccache.AggregationType,
	coldStartFilter bool) slov1alpha1.ResourceMap {
	rm := slov1alpha1.ResourceMap{}

	queryParam := metriccache.QueryParam{
		Start:     &start,
		End:       &end,
		Aggregate: aggregateType,
	}
	cpuAndMem, duration, err := r.collectNodeMetric(queryParam)
	if err != nil {
		klog.Warningf("query node metric failed, error %v", err)
		return rm
	}

	if coldStartFilter && metricsInColdStart(start, end, duration) {
		klog.V(4).Infof("metrics is in cold start, no need to report, current result sample duration %v",
			duration.String())
		return rm
	}

	rm.ResourceList = cpuAndMem

	value, exist := r.metricCache.Get(koordletutil.GPUDeviceType)
	if !exist {
		klog.V(5).Infof("got no device info on node, skip node gpu metric collection")
		return rm
	}
	gpus, ok := value.(koordletutil.GPUDevices)
	if !ok {
		klog.Errorf("value type error, expect: %T, got %T", koordletutil.GPUDevices{}, value)
		return rm
	}
	devices, err := r.collectNodeGPUMetric(queryParam, gpus)
	if err != nil {
		klog.Errorf("query node gpu metric failed, error: %v", err)
		return rm
	}
	rm.Devices = devices
	return rm
}

func metricsInColdStart(queryStart, queryEnd time.Time, duration time.Duration) bool {
	targetDuration := queryEnd.Sub(queryStart)
	return duration.Seconds() < targetDuration.Seconds()*validateTimeRangeRatio
}

func (r *nodeMetricInformer) collectNodeMetric(queryparam metriccache.QueryParam) (corev1.ResourceList, time.Duration, error) {
	rl := corev1.ResourceList{}
	querier, err := r.metricCache.Querier(*queryparam.Start, *queryparam.End)
	if err != nil {
		klog.V(5).Infof("get node metric querier failed, error %v", err)
		return rl, 0, err
	}

	cpuAggregateResult, err := doQuery(querier, metriccache.NodeCPUUsageMetric, nil)
	if err != nil {
		return rl, 0, err
	}
	cpuUsed, err := cpuAggregateResult.Value(queryparam.Aggregate)
	if err != nil {
		return rl, 0, err
	}

	memAggregateResult, err := doQuery(querier, metriccache.NodeMemoryUsageMetric, nil)
	if err != nil {
		return rl, 0, err
	}

	memUsed, err := memAggregateResult.Value(queryparam.Aggregate)
	if err != nil {
		return rl, 0, err
	}

	rl[corev1.ResourceCPU] = *resource.NewMilliQuantity(int64(cpuUsed*1000), resource.DecimalSI)
	rl[corev1.ResourceMemory] = *resource.NewQuantity(int64(memUsed), resource.BinarySI)

	return rl, cpuAggregateResult.TimeRangeDuration(), nil
}

func (r *nodeMetricInformer) collectNodeGPUMetric(queryparam metriccache.QueryParam, gpus koordletutil.GPUDevices) ([]schedulingv1alpha1.DeviceInfo, error) {
	result := make([]schedulingv1alpha1.DeviceInfo, 0)
	querier, err := r.metricCache.Querier(*queryparam.Start, *queryparam.End)
	if err != nil {
		klog.V(5).Infof("get node gpu metric querier failed, error %v", err)
		return nil, err
	}
	for _, gpu := range gpus {
		gpuCoreUsageAggregateResult, err := doQuery(querier, metriccache.NodeGPUCoreUsageMetric, metriccache.MetricPropertiesFunc.GPU(fmt.Sprintf("%d", gpu.Minor), gpu.UUID))
		if err != nil {
			return result, err
		}
		if gpuCoreUsageAggregateResult.Count() == 0 {
			continue
		}
		coreUsage, err := gpuCoreUsageAggregateResult.Value(queryparam.Aggregate)
		if err != nil {
			return result, err
		}
		gpuMemUsedAggregateResult, err := doQuery(querier, metriccache.NodeGPUMemUsageMetric, metriccache.MetricPropertiesFunc.GPU(fmt.Sprintf("%d", gpu.Minor), gpu.UUID))
		if err != nil {
			return result, err
		}
		if gpuMemUsedAggregateResult.Count() == 0 {
			continue
		}
		memUsage, err := gpuMemUsedAggregateResult.Value(queryparam.Aggregate)
		if err != nil {
			return result, err
		}
		memoryRatioRaw := 100 * memUsage / float64(gpu.MemoryTotal)
		minor := gpu.Minor
		result = append(result, schedulingv1alpha1.DeviceInfo{
			UUID:  gpu.UUID,
			Minor: pointer.Int32(minor),
			Type:  schedulingv1alpha1.GPU,
			// TODO: how to check the health status of GPU
			Resources: map[corev1.ResourceName]resource.Quantity{
				apiext.ResourceGPUCore:        *resource.NewQuantity(int64(coreUsage), resource.DecimalSI),
				apiext.ResourceGPUMemory:      *resource.NewQuantity(int64(memUsage), resource.BinarySI),
				apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(int64(memoryRatioRaw), resource.DecimalSI),
			},
		})
	}

	return result, nil
}

func (r *nodeMetricInformer) collectNodeAggregateMetric(endTime time.Time, aggregatePolicy *slov1alpha1.AggregatePolicy) []slov1alpha1.AggregatedUsage {
	var aggregateUsages []slov1alpha1.AggregatedUsage
	if aggregatePolicy == nil {
		return aggregateUsages
	}
	for _, d := range aggregatePolicy.Durations {
		start := endTime.Add(-d.Duration)
		aggregateUsage := slov1alpha1.AggregatedUsage{
			Usage: map[apiext.AggregationType]slov1alpha1.ResourceMap{
				apiext.P50: r.queryNodeMetric(start, endTime, metriccache.AggregationTypeP50, true),
				apiext.P90: r.queryNodeMetric(start, endTime, metriccache.AggregationTypeP90, true),
				apiext.P95: r.queryNodeMetric(start, endTime, metriccache.AggregationTypeP95, true),
				apiext.P99: r.queryNodeMetric(start, endTime, metriccache.AggregationTypeP99, true),
			},
			Duration: d,
		}
		aggregateUsages = append(aggregateUsages, aggregateUsage)
	}
	return aggregateUsages
}

func (r *nodeMetricInformer) collectPodMetric(podMeta *statesinformer.PodMeta, queryParam metriccache.QueryParam) (*slov1alpha1.PodMetricInfo, error) {
	if podMeta == nil || podMeta.Pod == nil {
		return nil, fmt.Errorf("invalid pod meta %v", podMeta)
	}
	podUID := string(podMeta.Pod.UID)

	querier, err := r.metricCache.Querier(*queryParam.Start, *queryParam.End)
	if err != nil {
		klog.V(5).Infof("failed to get querier for pod %s/%s, error %v", podMeta.Pod.Namespace, podMeta.Pod.Name, err)
		return nil, err
	}

	cpuAggregateResult, err := doQuery(querier, metriccache.PodCPUUsageMetric, metriccache.MetricPropertiesFunc.Pod(podUID))
	if err != nil {
		return nil, err
	}
	cpuUsed, err := cpuAggregateResult.Value(queryParam.Aggregate)
	if err != nil {
		return nil, err
	}

	memAggregateResult, err := doQuery(querier, metriccache.PodMemUsageMetric, metriccache.MetricPropertiesFunc.Pod(podUID))
	if err != nil {
		return nil, err
	}

	memUsed, err := memAggregateResult.Value(queryParam.Aggregate)
	if err != nil {
		return nil, err
	}
	rtn := &slov1alpha1.PodMetricInfo{
		Namespace: podMeta.Pod.Namespace,
		Name:      podMeta.Pod.Name,
		PodUsage: slov1alpha1.ResourceMap{
			ResourceList: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpuUsed*1000), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(int64(memUsed), resource.BinarySI),
			},
		},
	}
	return rtn, nil
}

func (r *nodeMetricInformer) collectPodGPUMetric(queryparam metriccache.QueryParam, uid string, gpus koordletutil.GPUDevices) ([]schedulingv1alpha1.DeviceInfo, error) {
	result := make([]schedulingv1alpha1.DeviceInfo, 0)
	querier, err := r.metricCache.Querier(*queryparam.Start, *queryparam.End)
	if err != nil {
		klog.V(5).Infof("get pod gpu metric querier failed, error %v", err)
		return nil, err
	}
	for _, gpu := range gpus {
		properties := metriccache.MetricPropertiesFunc.PodGPU(uid, fmt.Sprintf("%d", gpu.Minor), gpu.UUID)
		gpuCoreUsageAggregateResult, err := doQuery(querier, metriccache.PodGPUCoreUsageMetric, properties)
		if err != nil {
			return result, err
		}
		if gpuCoreUsageAggregateResult.Count() == 0 {
			continue
		}
		coreUsage, err := gpuCoreUsageAggregateResult.Value(queryparam.Aggregate)
		if err != nil {
			return result, err
		}
		gpuMemUsedAggregateResult, err := doQuery(querier, metriccache.PodGPUMemUsageMetric, properties)
		if err != nil {
			return result, err
		}
		if gpuMemUsedAggregateResult.Count() == 0 {
			continue
		}
		memUsage, err := gpuMemUsedAggregateResult.Value(queryparam.Aggregate)
		if err != nil {
			return result, err
		}
		memoryRatioRaw := 100 * memUsage / float64(gpu.MemoryTotal)
		minor := gpu.Minor
		result = append(result, schedulingv1alpha1.DeviceInfo{
			UUID:  gpu.UUID,
			Minor: pointer.Int32(minor),
			Type:  schedulingv1alpha1.GPU,
			// TODO: how to check the health status of GPU
			Resources: map[corev1.ResourceName]resource.Quantity{
				apiext.ResourceGPUCore:        *resource.NewQuantity(int64(coreUsage), resource.DecimalSI),
				apiext.ResourceGPUMemory:      *resource.NewQuantity(int64(memUsage), resource.BinarySI),
				apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(int64(memoryRatioRaw), resource.DecimalSI),
			},
		})
	}

	return result, nil
}

func (r *nodeMetricInformer) fillGPUMetrics(queryparam metriccache.QueryParam, info *slov1alpha1.PodMetricInfo, uid string, gpus koordletutil.GPUDevices) {
	podGPUMetrics, err := r.collectPodGPUMetric(queryparam, uid, gpus)
	if err != nil {
		klog.Warningf("collect pod UID(%s) gpu metric failed, error: %v", uid, err)
		return
	}

	info.PodUsage.Devices = podGPUMetrics
}

const (
	statusUpdateQPS   = 0.1
	statusUpdateBurst = 2
)

type statusUpdater struct {
	nodeMetricClient  clientsetv1alpha1.NodeMetricInterface
	previousTimestamp time.Time
	rateLimiter       *rate.Limiter
}

func newStatusUpdater(nodeMetricClient clientsetv1alpha1.NodeMetricInterface) *statusUpdater {
	return &statusUpdater{
		nodeMetricClient:  nodeMetricClient,
		previousTimestamp: time.Now().Add(-time.Hour * 24),
		rateLimiter:       rate.NewLimiter(statusUpdateQPS, statusUpdateBurst),
	}
}

func (su *statusUpdater) updateStatus(nodeMetric *slov1alpha1.NodeMetric, newStatus *slov1alpha1.NodeMetricStatus) error {
	if !su.rateLimiter.Allow() {
		return fmt.Errorf("updating status is limited qps=%v burst=%v", statusUpdateQPS, statusUpdateBurst)
	}

	newNodeMetric := nodeMetric.DeepCopy()
	newNodeMetric.Status = *newStatus

	_, err := su.nodeMetricClient.UpdateStatus(context.TODO(), newNodeMetric, metav1.UpdateOptions{})
	su.previousTimestamp = time.Now()
	return err
}

func doQuery(querier metriccache.Querier, resource metriccache.MetricResource, properties map[metriccache.MetricProperty]string) (metriccache.AggregateResult, error) {
	queryMeta, err := resource.BuildQueryMeta(properties)
	if err != nil {
		return nil, err
	}

	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err = querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return nil, err
	}

	return aggregateResult, nil
}
