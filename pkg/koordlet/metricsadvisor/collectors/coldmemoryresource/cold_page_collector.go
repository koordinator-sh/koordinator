package coldmemoryresource

import (
	"time"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"go.uber.org/atomic"
)

const (
	CollectorName = "coldPageCollector"
)

type ConcreteColdPageCollector interface {
	Run(stopCh <-chan struct{})
	Started() bool
}

type coldPageCollector struct {
	collectInterval   time.Duration
	cgroupReader      resourceexecutor.CgroupReader
	statesInformer    statesinformer.StatesInformer
	podFilter         framework.PodFilter
	appendableDB      metriccache.Appendable
	metricDB          metriccache.MetricCache
	coldPageCollector ConcreteColdPageCollector
}

func New(opt *framework.Options) framework.Collector {
	podFilter := framework.DefaultPodFilter
	if filter, ok := opt.PodFilters[CollectorName]; ok {
		podFilter = filter
	}
	return &coldPageCollector{
		collectInterval: opt.Config.CollectResUsedInterval,
		cgroupReader:    opt.CgroupReader,
		statesInformer:  opt.StatesInformer,
		podFilter:       podFilter,
		appendableDB:    opt.MetricCache,
		metricDB:        opt.MetricCache,
	}
}
func (c *coldPageCollector) Enabled() bool {
	// check enable
	// check kidled cold page collector
	if koordletutil.IsKidledSupported() {
		c.coldPageCollector = &kidledcoldPageCollector{
			collectInterval: c.collectInterval,
			cgroupReader:    c.cgroupReader,
			statesInformer:  c.statesInformer,
			podFilter:       c.podFilter,
			appendableDB:    c.appendableDB,
			metricDB:        c.metricDB,
			started:         atomic.NewBool(false),
		}
		return true
	}
	// TODO check kstaled cold page collector
	// TODO check DAMON cold page collector
	return false
}
func (c *coldPageCollector) Setup(c1 *framework.Context) {}
func (c *coldPageCollector) Run(stopCh <-chan struct{}) {
	c.coldPageCollector.Run(stopCh)
}
func (c *coldPageCollector) Started() bool {
	return c.coldPageCollector.Started()
}
