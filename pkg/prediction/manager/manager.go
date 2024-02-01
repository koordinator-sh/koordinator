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

package manager

import (
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/apis"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/checkpoint"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/metricscollector"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/profiler"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/workloadfetcher"
)

type PredictionManager interface {
	Run() error
	Started() bool
	Register(apis.ProfileKey, apis.PredictionProfileSpec) error
	Unregister(apis.ProfileKey) error
	GetResult(apis.ProfileKey, apis.ProfileResult, *apis.GetResultOptions) error
}

// Options are the arguments for creating a new Manager.
type Options struct {
}

func New(opt Options) PredictionManager {
	return &predictionMgrImpl{}
}

var _ PredictionManager = &predictionMgrImpl{}

type predictionMgrImpl struct {
	metricsServerRepo   metricscollector.MetricsServerRepository
	checkpoint          checkpoint.Checkpoint
	KubeWorkloadFetcher workloadfetcher.KubeWorkloadFetcher

	profilers map[apis.ProfileKey]profiler.Profiler
}

func (p *predictionMgrImpl) Run() error {
	// run checkpoint to load all history checkpoints
	// start workload fetcher syncing workloads
	// start metrics repo ready for collect metric
	// start profiler to generate prediction result
	panic("implement me")
}

func (p *predictionMgrImpl) Started() bool {
	// return true only if all components are started
	panic("implement me")
}

func (p *predictionMgrImpl) Register(key apis.ProfileKey, profile apis.PredictionProfileSpec) error {
	// return error if not started
	// update profile if already registered
	// create profiler if not exists
	panic("implement me")
}

func (p *predictionMgrImpl) Unregister(keys apis.ProfileKey) error {
	// return error if not started
	// return error if not registered
	// remove profile from map
	panic("implement me")
}

func (p *predictionMgrImpl) GetResult(key apis.ProfileKey, result apis.ProfileResult, opt *apis.GetResultOptions) error {
	// return error if not started
	// return error if not registered
	// get result from profiler and return
	panic("implement me")
}
