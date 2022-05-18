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

package options

import (
	scheduledoptions "k8s.io/kubernetes/cmd/kube-scheduler/app/options"

	schedulerappconfig "github.com/koordinator-sh/koordinator/cmd/koord-scheduler/app/config"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

// Options has all the params needed to run a Scheduler
type Options struct {
	*scheduledoptions.Options
}

// NewOptions returns default scheduler app options.
func NewOptions() *Options {
	return &Options{
		Options: scheduledoptions.NewOptions(),
	}
}

// Config return a scheduler config object
func (o *Options) Config() (*schedulerappconfig.Config, error) {
	config, err := o.Options.Config()
	if err != nil {
		return nil, err
	}

	koordinatorClient, err := koordinatorclientset.NewForConfig(config.KubeConfig)
	if err != nil {
		return nil, err
	}

	koordinatorSharedInformerFactory := koordinatorinformers.NewSharedInformerFactoryWithOptions(koordinatorClient, 0)

	return &schedulerappconfig.Config{
		Config:                           config,
		KoordinatorClient:                koordinatorClient,
		KoordinatorSharedInformerFactory: koordinatorSharedInformerFactory,
	}, nil
}
