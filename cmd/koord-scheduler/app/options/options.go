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
	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/runtime"
	apiserveroptions "k8s.io/apiserver/pkg/server/options"
	scheduleroptions "k8s.io/kubernetes/cmd/kube-scheduler/app/options"

	schedulerappconfig "github.com/koordinator-sh/koordinator/cmd/koord-scheduler/app/config"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
)

// Options has all the params needed to run a Scheduler
type Options struct {
	*scheduleroptions.Options
	CombinedInsecureServing *CombinedInsecureServingOptions
}

// NewOptions returns default scheduler app options.
func NewOptions() *Options {
	options := &Options{
		Options: scheduleroptions.NewOptions(),
		CombinedInsecureServing: &CombinedInsecureServingOptions{
			Healthz: (&apiserveroptions.DeprecatedInsecureServingOptions{
				BindNetwork: "tcp",
			}).WithLoopback(),
		},
	}
	options.CombinedInsecureServing.AddFlags(options.Flags.FlagSet("insecure serving"))
	return options
}

func (o *Options) Validate() []error {
	errs := o.Options.Validate()
	errs = append(errs, o.CombinedInsecureServing.Validate()...)
	return errs
}

// Config return a scheduler config object
func (o *Options) Config() (*schedulerappconfig.Config, error) {
	config, err := o.Options.Config()
	if err != nil {
		return nil, err
	}

	// use json for CRD clients
	kubeConfig := *config.KubeConfig
	kubeConfig.ContentType = runtime.ContentTypeJSON
	kubeConfig.AcceptContentTypes = runtime.ContentTypeJSON
	koordinatorClient, err := koordinatorclientset.NewForConfig(&kubeConfig)
	if err != nil {
		return nil, err
	}
	koordinatorSharedInformerFactory := koordinatorinformers.NewSharedInformerFactoryWithOptions(koordinatorClient, 0)

	appConfig := &schedulerappconfig.Config{
		Config:                           config,
		ServicesEngine:                   services.NewEngine(gin.New()),
		KoordinatorClient:                koordinatorClient,
		KoordinatorSharedInformerFactory: koordinatorSharedInformerFactory,
	}

	if err := o.CombinedInsecureServing.ApplyTo(appConfig, &config.ComponentConfig); err != nil {
		return nil, err
	}

	return appConfig, nil
}
