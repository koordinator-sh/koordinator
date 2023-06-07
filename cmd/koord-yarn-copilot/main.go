package main

import (
	"flag"

	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/koordinator-sh/koordinator/cmd/koord-yarn-copilot/options"
	"github.com/koordinator-sh/koordinator/pkg/yarn/copilot/nm"
	"github.com/koordinator-sh/koordinator/pkg/yarn/copilot/server"
)

func main() {
	flag.StringVar(&options.ServerEndpoint, "server-endpoint", options.DefaultServerEndpoint, "yarn copilot server endpoint.")
	flag.StringVar(&options.YarnContainerCgroupPath, "yarn-container-cgroup-path", options.DefaultYarnContainerCgroupPath, "yarn container cgroup path.")
	flag.StringVar(&options.NodeMangerEndpoint, "node-manager-endpoint", options.DefaultNodeManagerEndpoint, "node manger endpoint")
	flag.BoolVar(&options.SyncMemoryCgroup, "sync-memory-cgroup", false, "true to sync cpu cgroup info to memory, used for hadoop 2.x")
	flag.DurationVar(&options.SyncCgroupPeriod, "sync-cgroup-period", options.DefaultSyncCgroupPeriod, "period of resync all cpu/memory cgroup")

	stopCtx := signals.SetupSignalHandler()

	operator, err := nm.NewNodeMangerOperator("", options.YarnContainerCgroupPath, options.SyncMemoryCgroup, options.NodeMangerEndpoint, options.SyncCgroupPeriod)
	if err != nil {
		klog.Fatal(err)
	}
	go operator.Run(stopCtx.Done())
	err = server.NewYarnCopilotServer(operator, options.DefaultServerEndpoint).Run(stopCtx)
	if err != nil {
		klog.Fatal(err)
	}
}
