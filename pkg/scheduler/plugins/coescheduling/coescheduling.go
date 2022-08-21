package coescheduling

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	pgclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	pginformer "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coescheduling/core"
)

// Coscheduling is a plugin that schedules pods in a group.
type Coscheduling struct {
	frameworkHandler framework.Handle
	pgMgr            core.Manager
	scheduleTimeout  *time.Duration
}

var _ framework.QueueSortPlugin = &Coscheduling{}
var _ framework.PreFilterPlugin = &Coscheduling{}
var _ framework.PostFilterPlugin = &Coscheduling{}
var _ framework.PermitPlugin = &Coscheduling{}
var _ framework.ReservePlugin = &Coscheduling{}
var _ framework.PostBindPlugin = &Coscheduling{}
var _ framework.EnqueueExtensions = &Coscheduling{}

const (
	// Name is the name of the plugin used in Registry and configurations.
	Name = "Coscheduling"
)

// New initializes and returns a new Coscheduling plugin.
func New(obj runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	args, ok := obj.(*schedulingconfig.CoschedulingArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type CoschedulingArgs, got %T", obj)
	}

	pgClient := pgclientset.NewForConfigOrDie(handle.KubeConfig())
	pgInformerFactory := pginformer.NewSharedInformerFactory(pgClient, 0)
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()
	podInformer := handle.SharedInformerFactory().Core().V1().Pods()

	scheduleTimeDuration := time.Duration(args.PermitWaitingTimeSeconds) * time.Second

	ctx := context.TODO()

	pgMgr := core.NewPodGroupManager(pgClient, handle.SnapshotSharedLister(), &scheduleTimeDuration, pgInformer, podInformer, args)
	plugin := &Coscheduling{
		frameworkHandler: handle,
		pgMgr:            pgMgr,
		scheduleTimeout:  &scheduleTimeDuration,
	}
	// addEventHandler funcs
	pgInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    pgMgr.onPodGroupAdd,
			UpdateFunc: pgMgr.onPodGroupUpdate,
			DeleteFunc: pgMgr.onPodGroupDelete,
		},
	)
	podInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    pgMgr.onPodAdd,
			UpdateFunc: pgMgr.onPodUpdate,
			DeleteFunc: pgMgr.onPodDelete,
		},
	)
	pgInformerFactory.Start(ctx.Done())
	handle.SharedInformerFactory().Start(ctx.Done())

	pgInformerFactory.WaitForCacheSync(ctx.Done())
	handle.SharedInformerFactory().WaitForCacheSync(ctx.Done())

	pgMgr.Recover()

	return plugin, nil
}
