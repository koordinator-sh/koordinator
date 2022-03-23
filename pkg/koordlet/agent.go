package agent

import (
	"fmt"
	"os"
	"time"

	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"

	clientsetbeta1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/config"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/pleg"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/pluginsmgr"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/reporter"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

var (
	scheme = apiruntime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

type Daemon interface {
	Run(stopCh <-chan struct{})
}

type daemon struct {
	collector      metricsadvisor.Collector
	statesInformer statesinformer.StatesInformer
	metricCache    metriccache.MetricCache
	reporter       reporter.Reporter
	pluginsManager pluginsmgr.PluginsManager
	pleg           pleg.Pleg
}

func NewDaemon(config *config.Configuration) (Daemon, error) {
	// get node name
	nodeName := os.Getenv("NODE_NAME")
	if len(nodeName) == 0 {
		return nil, fmt.Errorf("failed to new daemon: NODE_NAME env is empty")
	}
	klog.Infof("NODE_NAME is %v,start time %v", nodeName, float64(time.Now().Unix()))
	metrics.RecordKoordletStartTime(nodeName, float64(time.Now().Unix()))

	klog.Infof("sysconf: %+v,agentMode:%v", sysutil.Conf, sysutil.AgentMode)
	klog.Infof("kernel version INFO : %+v", sysutil.HostSystemInfo)

	// setup cgroup path formatter from cgroup driver type
	var detectCgroupDriver sysutil.CgroupDriverType
	if pollErr := wait.PollImmediate(time.Second*10, time.Minute, func() (bool, error) {
		driver := sysutil.GuessCgroupDriverFromCgroupName()
		if driver.Validate() {
			detectCgroupDriver = driver
			return true, nil
		}
		klog.Infof("can not detect cgroup driver from 'kubepods' cgroup name")

		if driver, err := sysutil.GuessCgroupDriverFromKubelet(); err == nil && driver.Validate() {
			detectCgroupDriver = driver
			return true, nil
		} else {
			klog.Errorf("guess kubelet cgroup driver failed, retry...: %v", err)
			return false, nil
		}
	}); pollErr != nil {
		return nil, fmt.Errorf("can not detect kubelet cgroup driver: %v", pollErr)
	}
	sysutil.SetupCgroupPathFormatter(detectCgroupDriver)
	klog.Infof("Node %s use '%s' as cgroup driver", nodeName, string(detectCgroupDriver))

	kubeClient := clientset.NewForConfigOrDie(config.KubeRestConf)
	crdClient := clientsetbeta1.NewForConfigOrDie(config.KubeRestConf)

	pleg, err := pleg.NewPLEG(sysutil.Conf.CgroupRootDir)
	if err != nil {
		return nil, err
	}

	metaService := statesinformer.NewMetaService(config.MetaServiceConf, kubeClient, pleg, nodeName)
	metricCache, err := metriccache.NewMetricCache(config.MetricCacheConf)
	if err != nil {
		return nil, err
	}

	collectorService := metricsadvisor.NewCollector(config.CollectorConf, metaService, metricCache)
	reporterService := reporter.NewReporter(config.ReporterConf, kubeClient, crdClient, nodeName, metricCache, metaService)
	if err != nil {
		return nil, err
	}

	d := &daemon{
		collector:      collectorService,
		statesInformer: metaService,
		metricCache:    metricCache,
		reporter:       reporterService,
	}

	return d, nil
}

func (d *daemon) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	klog.Infof("Starting daemon")

	go func() {
		if err := d.metricCache.Run(stopCh); err != nil {
			klog.Error("Unable to run the metric cache: ", err)
			os.Exit(1)
		}
	}()

	// start meta service
	go func() {
		if err := d.statesInformer.Run(stopCh); err != nil {
			klog.Error("Unable to run the meta service: ", err)
			os.Exit(1)
		}
	}()

	// start collector
	go func() {
		if err := d.collector.Run(stopCh); err != nil {
			klog.Error("Unable to run the collector: ", err)
			os.Exit(1)
		}
	}()

	// TODO add HasSync function for collector
	klog.Infof("waiting 10 seconds for collector synced before start reporter")
	time.Sleep(10 * time.Second)

	// start reporter
	go func() {
		if err := d.reporter.Run(stopCh); err != nil {
			klog.Error("Unable to run the reporter: ", err)
			os.Exit(1)
		}
	}()

	// start resmanager
	go func() {
		if err := d.pluginsManager.Run(stopCh); err != nil {
			klog.Error("Unable to run the resManager: ", err)
			os.Exit(1)
		}
	}()

	// start slo control logic

	klog.Info("Start daemon successfully")
	<-stopCh
	klog.Info("Shutting down daemon")
}
