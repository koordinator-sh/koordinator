package coldmemoryresource

import (
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"go.uber.org/atomic"
)

const (
	CollectorName = "coldPageCollector"
)

type nonCollector struct {
}

func New(opt *framework.Options) framework.Collector {
	// check whether support kidled cold page info collector
	if koordletutil.IsKidledSupported() {
		return &kidledcoldPageCollector{
			collectInterval: opt.Config.CollectResUsedInterval,
			cgroupReader:    opt.CgroupReader,
			statesInformer:  opt.StatesInformer,
			podFilter:       framework.DefaultPodFilter,
			appendableDB:    opt.MetricCache,
			metricDB:        opt.MetricCache,
			started:         atomic.NewBool(false),
		}
	}
	// TODO(BUPT-wxq): check kstaled cold page collector
	// TODO(BUPT-wxq): check DAMON cold page collector
	// nonCollector does nothing
	return &nonCollector{}
}
func (n *nonCollector) Run(stopCh <-chan struct{}) {}
func (n *nonCollector) Started() bool {
	return false
}
func (n *nonCollector) Enabled() bool {
	return false
}
func (n *nonCollector) Setup(c1 *framework.Context) {}
