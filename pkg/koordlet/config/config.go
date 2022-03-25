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
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

type Configuration struct {
	KubeRestConf    *rest.Config
	MetaServiceConf *statesinformer.Config
	ReporterConf    *reporter.Config
	CollectorConf   *metricsadvisor.Config
	MetricCacheConf *metriccache.Config
	AuditConf       *audit.Config
	FeatureGates    map[string]bool
}

func NewConfiguration() *Configuration {
	return &Configuration{
		MetaServiceConf: statesinformer.NewDefaultConfig(),
		ReporterConf:    reporter.NewDefaultConfig(),
		CollectorConf:   metricsadvisor.NewDefaultConfig(),
		MetricCacheConf: metriccache.NewDefaultConfig(),
		AuditConf:       audit.NewDefaultConfig(),
	}
}

func (c *Configuration) InitFlags(fs *flag.FlagSet) {
	sysutil.Conf.InitFlags(fs)
	c.MetaServiceConf.InitFlags(fs)
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
