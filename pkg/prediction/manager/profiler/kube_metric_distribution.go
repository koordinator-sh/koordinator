/*
 Copyright 2024 The Koordinator Authors.

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
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/apis"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/model"
)

// KubeMetricDistribution is a profiler which generate distribution of metrics from k8s workloads
type KubeMetricDistribution struct {
	profileSpec *apis.PredictionProfileSpec

	metricModels map[string]model.DistributionModel
}

func (k *KubeMetricDistribution) Update(profileSpec *apis.PredictionProfileSpec) error {
	panic("implement me")
}

func (k *KubeMetricDistribution) Profile() error {
	// get ControllerWorkloadStatus list from workload fetcher
	// create model for each container and metrics
	// load history snapshot from checkpoint if exist
	// for each model, get metric from metric repo and feed samples to model
	// save checkpoint for each model if needed
	panic("implement me")
}

func (k *KubeMetricDistribution) GetResult() (apis.DistributionProfilerResult, error) {
	panic("implement me")
}
