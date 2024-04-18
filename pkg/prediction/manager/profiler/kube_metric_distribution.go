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

package profiler

import (
	"fmt"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/apis"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/checkpoint"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/metricscollector"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/model"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/workloadfetcher"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sync"
)

// KubeMetricDistribution is a profiler which generate distribution of metrics from k8s workloads
type KubeMetricDistribution struct {
	workloadFetcher   workloadfetcher.KubeWorkloadFetcher
	metricsServerRepo metricscollector.MetricsServerRepository
	checkpoint        checkpoint.Checkpoint

	key apis.ProfileKey

	profileMutex sync.RWMutex
	profileSpec  *apis.PredictionProfileSpec

	modelsMutex        sync.RWMutex
	distributionModels map[apis.HierarchyIdentifier]map[apis.MetricIdentifier]model.MetricDistributionModel // <id, map[metricName]model>
}

func NewKubeMetricDistribution(key apis.ProfileKey, profileSpec *apis.PredictionProfileSpec,
	metricsServerRepo metricscollector.MetricsServerRepository,
	kubeWorkloadFetcher workloadfetcher.KubeWorkloadFetcher,
	checkpoint checkpoint.Checkpoint) (*KubeMetricDistribution, error) {
	return &KubeMetricDistribution{
		key:         key,
		profileSpec: profileSpec,
	}, nil
}

func (k *KubeMetricDistribution) Update(profileSpec *apis.PredictionProfileSpec) error {
	k.profileMutex.Lock()
	defer k.profileMutex.Unlock()
	panic("implement me")
}

func (k *KubeMetricDistribution) Profile() error {
	// get ControllerWorkloadStatus list from workload fetcher
	// create/update model for each container and metrics
	// load history snapshot from checkpoint if exist
	// for each model, get metric from metric repo and feed samples to model
	// save checkpoint for each model if needed

	k.profileMutex.RLock()
	defer k.profileMutex.RUnlock()

	detail, err := k.getWorkloadDetail()
	if err != nil {
		return err
	}

	k.refreshModels(detail)

	for key, metricModels := range k.distributionModels {
		for _, modelDetail := range metricModels {
			if loaded, err := modelDetail.LoadHistoryIfNecessary(k.checkpoint); err != nil {
				klog.Warningf("failed to load snapshot for model %v: %v", key, err)
				continue
			} else {
				klog.V(5).Infof("history snapshot for model %v loaded: %v", key, loaded)
			}

			if err := modelDetail.FeedSamples(); err != nil {
				klog.Warningf("failed to load latest sample for model %v: %v", key, err)
				continue
			}

			if err := modelDetail.SaveSnapshot(k.checkpoint); err != nil {
				klog.Warningf("failed to save snapshot for model %v: %v", key, err)
				continue
			}

			klog.V(5).Infof("model %v: %v", key, modelDetail)
		}
	}

	return nil
}

func (k *KubeMetricDistribution) GetResult(result apis.ProfileResult) error {
	distributionResult, ok := result.(*apis.DistributionProfilerResult)
	if !ok {
		return fmt.Errorf("invalid result type %T", result)
	}

	distributionResult.ProfileKey = k.key

	k.modelsMutex.RLock()
	defer k.modelsMutex.RUnlock()
	distributionResult.Models = make([]apis.DistributionModelDetail, 0, len(k.distributionModels))
	for id, models := range k.distributionModels {
		modelResult := apis.DistributionModelDetail{
			ID:            id,
			Distributions: make([]apis.MetricDistribution, 0, len(models)),
		}
		for _, m := range models {
			detail := apis.MetricDistribution{}
			if err := m.GetDistribution(&detail); err == nil {
				modelResult.Distributions = append(modelResult.Distributions, detail)
			} else {
				klog.Warningf("failed to get distribution for model id %v, metric %v, error %v", id, m.Key(), err)
			}
		}
		distributionResult.Models = append(distributionResult.Models, modelResult)
	}
	return nil
}

func (k *KubeMetricDistribution) getWorkloadDetail() (*workloadDetail, error) {
	k.profileMutex.RLock()
	defer k.profileMutex.RUnlock()
	switch k.profileSpec.Target.Type {
	case apis.WorkloadTargetController:
		return getControllerWorkloadDetail(k.workloadFetcher, k.profileSpec.Target.Controller)
	default:
		return nil, fmt.Errorf("invalid target type %v", k.profileSpec.Target.Type)
	}
}

func (k *KubeMetricDistribution) refreshModels(detail *workloadDetail) {
	k.profileMutex.RLock()
	defer k.profileMutex.RUnlock()
	// record new target metrics according to çurrent profile
	newTargetMetrics := make(map[apis.HierarchyIdentifier]map[string]struct{})

	targetIDs, err := getTargetIdentifiers(&k.profileSpec.Target, detail)
	if err != nil {
		klog.Warningf("failed to get target identifiers for profile %v %v", k.key.Key(), err)
		return
	}

	k.modelsMutex.Lock()
	defer k.modelsMutex.Unlock()
	for _, targetID := range targetIDs {
		if _, targetExist := newTargetMetrics[targetID]; !targetExist {
			newTargetMetrics[targetID] = map[string]struct{}{}
		}

		// metrics server metrics
		if k.profileSpec.Metric.MetricServer != nil {
			for _, metricName := range k.profileSpec.Metric.MetricServer.Names {

				metricID := apis.MetricIdentifier{
					Source: apis.MetricSourceTypeMetricsAPI,
					Name:   string(metricName),
				}

				if _, metricExist := newTargetMetrics[targetID][metricID.Name]; !metricExist {
					newTargetMetrics[targetID][metricID.Name] = struct{}{}
				}

				var distributionModel model.MetricDistributionModel
				exist := false
				if distributionModel, exist = k.distributionModels[targetID][metricID]; !exist {
					// create and insert new model
				}

				feeder, err := k.getModelFeeder(targetID, detail, metricID.Name)
				if err != nil {
					klog.Warningf("failed to get model feeder for model id %v, metric %v, error %v", targetID, metricID, err)
					continue
				}
				distributionModel.PrepareFeeder(feeder)
			}
		}
		// prometheus metrics
	}

	// delete models if not in newTargetMetrics
	for targetID, metricModels := range k.distributionModels {
		if _, targetExist := newTargetMetrics[targetID]; !targetExist {
			delete(k.distributionModels, targetID)
		}
		for metricID, _ := range metricModels {
			if _, metricExist := newTargetMetrics[targetID][metricID.Name]; !metricExist {
				delete(k.distributionModels[targetID], metricID)
			}
		}
	}

}

func (k *KubeMetricDistribution) getModelFeeder(id apis.HierarchyIdentifier, workload *workloadDetail, metricName string) (*model.Feeder, error) {
	feeder := &model.Feeder{}
	switch id.Level {
	case apis.ProfileHierarchyLevelContainer:
		feeder.LoadSamples = model.FeedContainerMetricsFromMetricAPIFunc(k.metricsServerRepo, id.Name, workload.pods, metricName)
	default:
		return nil, fmt.Errorf("invalid hierarchy level %v", id.Level)
	}
	return feeder, nil
}

func getTargetIdentifiers(target *apis.WorkloadTarget, detail *workloadDetail) ([]apis.HierarchyIdentifier, error) {
	switch target.Type {
	case apis.WorkloadTargetController:
		switch target.Controller.Hierarchy.Level {
		case apis.ProfileHierarchyLevelContainer:
			ids := make([]apis.HierarchyIdentifier, 0, len(detail.containers))
			for _, containerName := range detail.containers {
				ids = append(ids, apis.HierarchyIdentifier{
					Level: apis.ProfileHierarchyLevelContainer,
					Name:  containerName,
				})
			}
			return ids, nil
		default:
			return nil, fmt.Errorf("invalid hierarchy level %v", target.Controller.Hierarchy.Level)
		}
	default:
		return nil, fmt.Errorf("invalid target type %v", target.Type)
	}
}

func getControllerWorkloadDetail(fetcher workloadfetcher.KubeWorkloadFetcher,
	ref *apis.ControllerRef) (*workloadDetail, error) {
	detail := &workloadDetail{
		workload: types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name},
	}
	if pods, err := fetcher.GetPodsByWorkload(ref); err == nil {
		for _, pod := range pods {
			detail.pods = append(detail.pods, types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name})
		}
	} else {
		return nil, err
	}

	if podTemplate, err := fetcher.GetPodTemplateOfWorkload(ref); err == nil {
		detail.resourceRequirement = parseContainerResourceRequirement(podTemplate)
	} else {
		return nil, err
	}

	for containerName := range detail.resourceRequirement {
		detail.containers = append(detail.containers, containerName)
	}

	return detail, nil
}

type workloadDetail struct {
	workload            types.NamespacedName
	containers          []string
	pods                []types.NamespacedName
	resourceRequirement map[string]corev1.ResourceRequirements // key is container name
}

func parseContainerResourceRequirement(pod *corev1.PodTemplateSpec) map[string]corev1.ResourceRequirements {
	result := make(map[string]corev1.ResourceRequirements)
	for _, container := range pod.Spec.Containers {
		result[container.Name] = container.Resources
	}
	return result
}
