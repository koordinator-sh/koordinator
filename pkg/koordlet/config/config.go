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

package config

import (
	"flag"
	"strings"

	"k8s.io/client-go/rest"
	cliflag "k8s.io/component-base/cli/flag"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/reporter"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resmanager"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

type Configuration struct {
	KubeRestConf       *rest.Config
	StatesInformerConf *statesinformer.Config
	ReporterConf       *reporter.Config
	CollectorConf      *metricsadvisor.Config
	MetricCacheConf    *metriccache.Config
	ResManagerConf     *resmanager.Config
	AuditConf          *audit.Config
	FeatureGates       map[string]bool
}

func NewConfiguration() *Configuration {
	return &Configuration{
		StatesInformerConf: statesinformer.NewDefaultConfig(),
		ReporterConf:       reporter.NewDefaultConfig(),
		CollectorConf:      metricsadvisor.NewDefaultConfig(),
		MetricCacheConf:    metriccache.NewDefaultConfig(),
		ResManagerConf:     resmanager.NewDefaultConfig(),
		AuditConf:          audit.NewDefaultConfig(),
	}
}

func (c *Configuration) InitFlags(fs *flag.FlagSet) {
	system.Conf.InitFlags(fs)
	c.StatesInformerConf.InitFlags(fs)
	c.ReporterConf.InitFlags(fs)
	c.CollectorConf.InitFlags(fs)
	c.MetricCacheConf.InitFlags(fs)
	c.AuditConf.InitFlags(fs)
	fs.Var(cliflag.NewMapStringBool(&c.FeatureGates), "feature-gates", "A set of key=value pairs that describe feature gates for alpha/experimental features. "+
		"Options are:\n"+strings.Join(features.DefaultKoordletFeatureGate.KnownFeatures(), "\n"))
}

func (c *Configuration) InitClient() error {
	cfg, err := config.GetConfig()
	if err != nil {
		return err
	}
	cfg.UserAgent = "koordlet"
	c.KubeRestConf = cfg
	return nil
}
