package main

import (
	"flag"
	"os"

	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/koordinator-sh/koordinator/cmd/koord-yarn-copilot/options"
	"github.com/koordinator-sh/koordinator/pkg/yarn/copilot/nm"
	"github.com/koordinator-sh/koordinator/pkg/yarn/copilot/server"
)

func main() {
	conf := options.NewConfiguration()
	klog.InitFlags(flag.CommandLine)
	flag.StringVar(&conf.ServerEndpoint, "server-endpoint", conf.ServerEndpoint, "yarn copilot server endpoint.")
	flag.StringVar(&conf.YarnContainerCgroupPath, "yarn-container-cgroup-path", conf.YarnContainerCgroupPath, "yarn container cgroup path.")
	flag.StringVar(&conf.NodeMangerEndpoint, "node-manager-endpoint", conf.NodeMangerEndpoint, "node manger endpoint")
	flag.BoolVar(&conf.SyncMemoryCgroup, "sync-memory-cgroup", conf.SyncMemoryCgroup, "true to sync cpu cgroup info to memory, used for hadoop 2.x")
	flag.DurationVar(&conf.SyncCgroupPeriod, "sync-cgroup-period", conf.SyncCgroupPeriod, "period of resync all cpu/memory cgroup")
	flag.StringVar(&conf.CgroupRootDir, "cgroup-root-dir", conf.CgroupRootDir, "cgroup root directory")
	help := flag.Bool("help", false, "help information")

	flag.Parse()
	if *help {
		flag.Usage()
		os.Exit(0)
	}
	flag.VisitAll(func(f *flag.Flag) {
		klog.Infof("args: %s = %s", f.Name, f.Value)
	})
	stopCtx := signals.SetupSignalHandler()

	operator, err := nm.NewNodeMangerOperator(conf.CgroupRootDir, conf.YarnContainerCgroupPath, conf.SyncMemoryCgroup, conf.NodeMangerEndpoint, conf.SyncCgroupPeriod)
	if err != nil {
		klog.Fatal(err)
	}
	go func() {
		if err := operator.Run(stopCtx.Done()); err != nil {
			klog.Error(err)
		}
	}()
	err = server.NewYarnCopilotServer(operator, conf.ServerEndpoint).Run(stopCtx)
	if err != nil {
		klog.Fatal(err)
	}
}
