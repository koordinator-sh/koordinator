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

package prom

import (
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type MetricType string

const (
	MetricTypeNode        MetricType = "node"         // target labels: none
	MetricTypeQoS         MetricType = "qos"          // target labels: <"qos">
	MetricTypePod         MetricType = "pod"          // target labels: <"namespace", "pod">
	MetricTypePodUID      MetricType = "pod_uid"      // target labels: <"pod_uid">
	MetricTypeContainer   MetricType = "container"    // target labels: <"namespace", "pod", "container">
	MetricTypeContainerID MetricType = "container_id" // target labels: <"container_id">
	// TODO: support hostApp type

	TargetLabelKeyQoS         model.LabelName = "qos"
	TargetLabelKeyPod         model.LabelName = "pod"
	TargetLabelKeyNamespace   model.LabelName = "namespace"
	TargetLabelKeyPodUID      model.LabelName = "uid"
	TargetLabelKeyContainer   model.LabelName = "container"
	TargetLabelKeyContainerID model.LabelName = "container_id"

	SampleKeyNode              = "__node__"
	SampleKeyQoSPrefix         = "__qos_"
	SampleKeyPodUIDPrefix      = "__pod_uid_"
	SampleKeyContainerIDPrefix = "__container_id_"
)

func IsValidMetricRule(metricRule *MetricRule) error {
	if len(metricRule.TargetName) <= 0 {
		return fmt.Errorf("empty metric target name")
	}
	if len(metricRule.SourceName) <= 0 {
		return fmt.Errorf("empty metric source name")
	}

	switch metricRule.TargetType {
	case MetricTypeNode:
		break
	case MetricTypeQoS:
		if len(metricRule.TargetLabels) <= 0 {
			break
		}
		if _, ok := metricRule.TargetLabels[TargetLabelKeyQoS]; !ok {
			return fmt.Errorf("metric type %s required target label key %s not found",
				metricRule.TargetType, TargetLabelKeyQoS)
		}
	case MetricTypePod:
		if len(metricRule.TargetLabels) <= 0 {
			break
		}
		_, NamespaceIsOK := metricRule.TargetLabels[TargetLabelKeyNamespace]
		_, podNameIsOK := metricRule.TargetLabels[TargetLabelKeyPod]
		if !NamespaceIsOK || !podNameIsOK {
			return fmt.Errorf("metric type %s required target label keys <%s, %s> not found",
				metricRule.TargetType, TargetLabelKeyNamespace, TargetLabelKeyPod)
		}
	case MetricTypePodUID:
		if len(metricRule.TargetLabels) <= 0 {
			break
		}
		_, podUIDIsOK := metricRule.TargetLabels[TargetLabelKeyPodUID]
		if !podUIDIsOK {
			return fmt.Errorf("metric type %s required target label keys  %s not found",
				metricRule.TargetType, TargetLabelKeyPodUID)
		}
	case MetricTypeContainer:
		if len(metricRule.TargetLabels) <= 0 {
			break
		}
		_, NamespaceIsOK := metricRule.TargetLabels[TargetLabelKeyNamespace]
		_, podNameIsOK := metricRule.TargetLabels[TargetLabelKeyPod]
		_, containerNameIsOK := metricRule.TargetLabels[TargetLabelKeyContainer]
		if !NamespaceIsOK || !podNameIsOK || !containerNameIsOK {
			return fmt.Errorf("metric type %s required target label keys <%s, %s, %s> not found",
				metricRule.TargetType, TargetLabelKeyNamespace, TargetLabelKeyPod, TargetLabelKeyContainer)
		}
	case MetricTypeContainerID:
		if len(metricRule.TargetLabels) <= 0 {
			break
		}
		_, containerIDIsOK := metricRule.TargetLabels[TargetLabelKeyContainerID]
		if !containerIDIsOK {
			return fmt.Errorf("metric type %s required target label keys %s not found",
				metricRule.TargetType, TargetLabelKeyContainerID)
		}
	default:
		return fmt.Errorf("unsupported metric target type %s", metricRule.TargetType)
	}

	return nil
}

func GenSampleKey(sample *model.Sample, metricType MetricType, targetLabels model.LabelSet) string {
	switch metricType {
	case MetricTypeNode:
		// e.g. "__node__"
		return SampleKeyNode
	case MetricTypeQoS:
		// e.g. "__qos_BE"
		qosKey := TargetLabelKeyQoS
		if len(targetLabels) > 0 {
			qosKey = model.LabelName(targetLabels[TargetLabelKeyQoS])
		}
		return SampleKeyQoSPrefix + string(sample.Metric[qosKey])
	case MetricTypePod:
		// e.g. "example-ns/example-pod"
		podNamespaceKey := TargetLabelKeyNamespace
		podNameKey := TargetLabelKeyPod
		if len(targetLabels) > 0 {
			if key, ok := targetLabels[TargetLabelKeyNamespace]; ok {
				podNamespaceKey = model.LabelName(key)
			}
			if key, ok := targetLabels[TargetLabelKeyPod]; ok {
				podNameKey = model.LabelName(key)
			}
		}
		return string(sample.Metric[podNamespaceKey]) + "/" + string(sample.Metric[podNameKey])
	case MetricTypePodUID:
		// e.g. "__pod_uid_xxxxxx"
		podUIDKey := TargetLabelKeyPodUID
		if len(targetLabels) > 0 {
			if key, ok := targetLabels[TargetLabelKeyPodUID]; ok {
				podUIDKey = model.LabelName(key)
			}
		}
		return SampleKeyPodUIDPrefix + string(sample.Metric[podUIDKey])
	case MetricTypeContainer:
		// e.g. "example-ns/example-pod/example-container"
		podNamespaceKey := TargetLabelKeyNamespace
		podNameKey := TargetLabelKeyPod
		containerNameKey := TargetLabelKeyContainer
		if len(targetLabels) > 0 {
			if key, ok := targetLabels[TargetLabelKeyNamespace]; ok {
				podNamespaceKey = model.LabelName(key)
			}
			if key, ok := targetLabels[TargetLabelKeyPod]; ok {
				podNameKey = model.LabelName(key)
			}
			if key, ok := targetLabels[TargetLabelKeyContainer]; ok {
				containerNameKey = model.LabelName(key)
			}
		}
		return string(sample.Metric[podNamespaceKey]) + "/" + string(sample.Metric[podNameKey]) + "/" + string(sample.Metric[containerNameKey])
	case MetricTypeContainerID:
		// e.g. "__container_id_containerd://yyyyyy"
		containerIDKey := TargetLabelKeyContainerID
		if len(targetLabels) > 0 {
			if key, ok := targetLabels[TargetLabelKeyContainerID]; ok {
				containerIDKey = model.LabelName(key)
			}
		}
		return SampleKeyContainerIDPrefix + string(sample.Metric[containerIDKey])
	}
	return ""
}

func MakeMetricSamples(samplesMap map[string][]*model.Sample, targetName string, targetType MetricType, node *corev1.Node, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error) {
	switch targetType {
	case MetricTypeNode:
		return GenerateNodeMetrics(samplesMap, targetName, node)
	case MetricTypeQoS:
		return GenerateQoSMetrics(samplesMap, targetName)
	case MetricTypePod:
		return GeneratePodMetrics(samplesMap, targetName, podMetas)
	case MetricTypePodUID:
		return GeneratePodUIDMetrics(samplesMap, targetName)
	case MetricTypeContainer:
		return GenerateContainerMetrics(samplesMap, targetName, podMetas)
	case MetricTypeContainerID:
		return GenerateContainerIDMetrics(samplesMap, targetName)
	}
	return nil, fmt.Errorf("unsupported metric type %s", targetType)
}

type ScraperConfig struct {
	Enable         bool
	Client         *ClientConfig
	Decoders       []*DecodeConfig
	ScrapeInterval time.Duration
	JobName        string
}

type ScraperFactory interface {
	New(clientConfig *ScraperConfig) (Scraper, error)
}

// Scraper is the interface for scraping the prometheus metrics and decoding into the metric cache's metric samples.
type Scraper interface {
	Name() string
	GetMetricSamples(node *corev1.Node, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error)
}

type GenericScraper struct {
	Client  ScrapeClient
	Decoder Decoder
	JobName string
	Enable  bool
}

func NewGenericScraper(cfg *ScraperConfig) (Scraper, error) {
	client, err := NewScrapeClientFromConfig(cfg.Client)
	if err != nil {
		return nil, fmt.Errorf("failed to create scrape client, cfg %+v, err: %w", cfg, err)
	}

	s := &GenericScraper{
		Client:  client,
		JobName: cfg.JobName,
		Enable:  cfg.Enable,
	}

	if len(cfg.Decoders) <= 0 {
		return nil, fmt.Errorf("no decoder config found")
	}

	s.Decoder = NewMultiDecoder(cfg.Decoders...)

	return s, nil
}

func (c *GenericScraper) Name() string {
	return c.JobName
}

func (c *GenericScraper) GetMetricSamples(node *corev1.Node, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error) {
	if !c.Enable {
		klog.V(6).Infof("scraper %s is disabled, skip scrape metrics", c.Name())
		return nil, nil
	}

	samples, err := c.Client.GetMetrics()
	if err != nil {
		return nil, fmt.Errorf("get metrics failed, url %s, err: %w", c.Client.URL(), err)
	}
	klog.V(6).Infof("scrape prom metrics finished by scraper %s, url %s, metrics num %d", c.Name(), c.Client.URL(), len(samples))

	metricSamples, err := c.Decoder.Decode(samples, node, podMetas)
	if err != nil {
		return nil, fmt.Errorf("decode metrics failed, err: %w", err)
	}

	klog.V(6).Infof("decode prom samples finished by scraper %s, metrics num %d", c.Name(), len(metricSamples))
	return metricSamples, nil
}

type DecodeConfig struct {
	TargetMetric   string
	TargetType     MetricType
	TargetLabels   model.LabelSet
	SourceMetric   string
	SourceLabels   model.LabelSet
	ValidTimeRange time.Duration
}

type Decoder interface {
	Decode(samples []*model.Sample, node *corev1.Node, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error)
}

type DecodeFn func(samples []*model.Sample, node *corev1.Node, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error)

type MultiDecoder struct {
	decoders []Decoder
}

func NewMultiDecoder(configs ...*DecodeConfig) Decoder {
	var decoders []Decoder
	for _, cfg := range configs {
		decoders = append(decoders, NewGenericDecoder(cfg))
	}
	return &MultiDecoder{
		decoders: decoders,
	}
}

func (m *MultiDecoder) Decode(samples []*model.Sample, node *corev1.Node, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error) {
	// TODO: look up multiple target metrics faster
	var allMetricSamples []metriccache.MetricSample
	var errs []error
	for _, decoder := range m.decoders {
		metricSamples, err := decoder.Decode(samples, node, podMetas)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		allMetricSamples = append(allMetricSamples, metricSamples...)
	}
	if len(errs) > 0 {
		return allMetricSamples, utilerrors.NewAggregate(errs)
	}
	return allMetricSamples, nil
}

type GenericDecoder struct {
	decodeFn DecodeFn
}

func NewGenericDecoder(cfg *DecodeConfig) Decoder {
	return &GenericDecoder{
		decodeFn: GenericDecodeFn(cfg),
	}
}

func (d *GenericDecoder) Decode(samples []*model.Sample, node *corev1.Node, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error) {
	return d.decodeFn(samples, node, podMetas)
}

func GenericDecodeFn(cfg *DecodeConfig) DecodeFn {
	return func(samples []*model.Sample, node *corev1.Node, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error) {
		timeNow := model.Now()
		samplesMap := map[string][]*model.Sample{}
		for _, sample := range samples {
			if string(sample.Metric[model.MetricNameLabel]) != cfg.SourceMetric {
				continue
			}
			// in case some metrics have no timestamp
			if sample.Timestamp.UnixNano() <= 0 {
				sample.Timestamp = timeNow
			} else if sample.Timestamp.Sub(timeNow) > cfg.ValidTimeRange || timeNow.Sub(sample.Timestamp) > cfg.ValidTimeRange {
				continue
			}

			labelsMatched := true
			for key, value := range cfg.SourceLabels {
				if sample.Metric[key] != value {
					labelsMatched = false
					break
				}
			}
			if !labelsMatched {
				continue
			}

			sampleKey := GenSampleKey(sample, cfg.TargetType, cfg.TargetLabels)
			vec, ok := samplesMap[sampleKey]
			if ok {
				samplesMap[sampleKey] = append(vec, sample)
			} else {
				samplesMap[sampleKey] = []*model.Sample{sample}
			}
		}

		klog.V(6).Infof("decode prom samples num %v for metric name %s, target %s, detail %+v",
			len(samplesMap), cfg.SourceMetric, cfg.TargetMetric, samplesMap)
		return MakeMetricSamples(samplesMap, cfg.TargetMetric, cfg.TargetType, node, podMetas)
	}
}

func GenerateNodeMetrics(samplesMap map[string][]*model.Sample, targetName string, _ *corev1.Node) ([]metriccache.MetricSample, error) {
	samples, ok := samplesMap[SampleKeyNode]
	if !ok || len(samples) <= 0 {
		klog.V(5).Infof("find no sample for node, sample num %s", len(samplesMap))
		return nil, nil
	}

	metricResource := metriccache.GenMetricResource(metriccache.MetricKind(targetName))
	var metricSamples []metriccache.MetricSample
	for _, sample := range samples {
		metricSample, err := metricResource.GenerateSample(nil, sample.Timestamp.Time(), float64(sample.Value))
		if err != nil {
			return nil, fmt.Errorf("failed to generate metric sample for node, metric %s, err: %w", targetName, err)
		}
		metricSamples = append(metricSamples, metricSample)
	}

	return metricSamples, nil
}

func GenerateQoSMetrics(samplesMap map[string][]*model.Sample, targetName string) ([]metriccache.MetricSample, error) {
	metricResource := metriccache.GenMetricResource(metriccache.MetricKind(targetName), metriccache.MetricPropertyQoS)
	var metricSamples []metriccache.MetricSample
	for sampleKey, samples := range samplesMap {
		if !strings.HasPrefix(sampleKey, SampleKeyQoSPrefix) {
			continue
		}

		qos := strings.TrimPrefix(sampleKey, SampleKeyQoSPrefix)
		if len(samples) <= 0 {
			klog.V(5).Infof("find no sample for qos %s, sample num %s", qos, len(samplesMap))
			continue
		}
		for _, sample := range samples {
			metricSample, err := metricResource.GenerateSample(metriccache.MetricPropertiesFunc.QoS(qos), sample.Timestamp.Time(), float64(sample.Value))
			if err != nil {
				return nil, fmt.Errorf("failed to generate metric sample for qos %s, metric %s, err: %w", qos, targetName, err)
			}
			metricSamples = append(metricSamples, metricSample)
		}
	}

	return metricSamples, nil
}

func GeneratePodMetrics(samplesMap map[string][]*model.Sample, targetName string, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error) {
	metricResource := metriccache.GenMetricResource(metriccache.MetricKind(targetName), metriccache.MetricPropertyPodUID)
	var metricSamples []metriccache.MetricSample
	for _, podMeta := range podMetas {
		samples, ok := samplesMap[podMeta.Key()]
		if !ok {
			klog.V(5).Infof("failed to find sample for pod %s, metric %s", podMeta.Key(), targetName)
			continue
		}

		for _, sample := range samples {
			metricSample, err := metricResource.GenerateSample(metriccache.MetricPropertiesFunc.Pod(string(podMeta.Pod.UID)), sample.Timestamp.Time(), float64(sample.Value))
			if err != nil {
				return nil, fmt.Errorf("failed to generate metric sample for pod %s, metric %s, err: %w", podMeta.Key(), targetName, err)
			}
			metricSamples = append(metricSamples, metricSample)
		}
	}

	return metricSamples, nil
}

func GeneratePodUIDMetrics(samplesMap map[string][]*model.Sample, targetName string) ([]metriccache.MetricSample, error) {
	metricResource := metriccache.GenMetricResource(metriccache.MetricKind(targetName), metriccache.MetricPropertyPodUID)
	var metricSamples []metriccache.MetricSample
	for sampleKey, samples := range samplesMap {
		if !strings.HasPrefix(sampleKey, SampleKeyPodUIDPrefix) {
			continue
		}

		uid := strings.TrimPrefix(sampleKey, SampleKeyPodUIDPrefix)
		if len(samples) <= 0 {
			klog.V(5).Infof("find no sample for pod uid %s, sample num %s", uid, len(samplesMap))
			continue
		}
		for _, sample := range samples {
			metricSample, err := metricResource.GenerateSample(metriccache.MetricPropertiesFunc.Pod(uid), sample.Timestamp.Time(), float64(sample.Value))
			if err != nil {
				return nil, fmt.Errorf("failed to generate metric sample for pod uid %s, metric %s, err: %w", uid, targetName, err)
			}
			metricSamples = append(metricSamples, metricSample)
		}
	}

	return metricSamples, nil
}

func GenerateContainerMetrics(samplesMap map[string][]*model.Sample, targetName string, podMetas []*statesinformer.PodMeta) ([]metriccache.MetricSample, error) {
	metricResource := metriccache.GenMetricResource(metriccache.MetricKind(targetName), metriccache.MetricPropertyContainerID)
	var metricSamples []metriccache.MetricSample
	for _, podMeta := range podMetas {
		for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
			if len(containerStat.ContainerID) <= 0 {
				klog.V(6).Infof("skip find sample for container %s/%s, metric %s, no container id", podMeta.Key(), containerStat.Name, targetName)
				continue
			}

			samples, ok := samplesMap[podMeta.Key()+"/"+containerStat.Name]
			if !ok {
				klog.V(5).Infof("failed to find sample for container %s/%s, metric %s", podMeta.Key(), containerStat.Name, targetName)
				continue
			}

			for _, sample := range samples {
				metricSample, err := metricResource.GenerateSample(metriccache.MetricPropertiesFunc.Container(containerStat.ContainerID), sample.Timestamp.Time(), float64(sample.Value))
				if err != nil {
					return nil, fmt.Errorf("failed to generate metric sample for container %s/%s, metric %s, err: %w", podMeta.Key(), containerStat.Name, targetName, err)
				}
				metricSamples = append(metricSamples, metricSample)
			}
		}
	}

	return metricSamples, nil
}

func GenerateContainerIDMetrics(samplesMap map[string][]*model.Sample, targetName string) ([]metriccache.MetricSample, error) {
	metricResource := metriccache.GenMetricResource(metriccache.MetricKind(targetName), metriccache.MetricPropertyContainerID)
	var metricSamples []metriccache.MetricSample
	for sampleKey, samples := range samplesMap {
		if !strings.HasPrefix(sampleKey, SampleKeyContainerIDPrefix) {
			continue
		}

		containerID := strings.TrimPrefix(sampleKey, SampleKeyContainerIDPrefix)
		if len(samples) <= 0 {
			klog.V(5).Infof("find no sample for container id %s, sample num %s", containerID, len(samplesMap))
			continue
		}
		for _, sample := range samples {
			metricSample, err := metricResource.GenerateSample(metriccache.MetricPropertiesFunc.Container(containerID), sample.Timestamp.Time(), float64(sample.Value))
			if err != nil {
				return nil, fmt.Errorf("failed to generate metric sample for container id %s, metric %s, err: %w", containerID, targetName, err)
			}
			metricSamples = append(metricSamples, metricSample)
		}
	}

	return metricSamples, nil
}
