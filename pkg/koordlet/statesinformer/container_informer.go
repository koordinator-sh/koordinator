package statesinformer

import (
	"context"
	"golang.org/x/time/rate"
	"k8s.io/klog/v2"
	"sync"
	"time"

	"go.uber.org/atomic"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/pleg"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime/handler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	ContainerInformerName pluginName = "ContainerInformer"
)

type ContainerInformer interface {
	Setup(ctx *pluginOption, state *pluginState)
	Start(stopCh <-chan struct{})
	HasSynced() bool
}

type createEvent struct {
	containerID string
	podID       string
}

type containerInformer struct {
	config *Config

	containerUpdatedTime time.Time
	containerRWMutex     sync.RWMutex
	containerMap         map[string]map[string]*ContainerMeta
	sandboxContainerMap  map[string]*ContainerMeta
	containerHasSynced   *atomic.Bool

	// use pleg to accelerate the efficiency of container update
	pleg             pleg.Pleg
	containerCreated chan createEvent

	runtimeServiceClient runtimeapi.RuntimeServiceClient
}

func NewContainerInformer() *containerInformer {
	p, err := pleg.NewPLEG(system.Conf.CgroupRootDir)
	if err != nil {
		klog.Fatalf("failed to create PLEG, %v", err)
	}

	informer := &containerInformer{
		containerMap:        make(map[string]map[string]*ContainerMeta),
		sandboxContainerMap: make(map[string]*ContainerMeta),
		containerHasSynced:  atomic.NewBool(false),
		containerCreated:    make(chan createEvent, 1),
		pleg:                p,
	}
	return informer
}

func (c *containerInformer) Setup(ctx *pluginOption, states *pluginState) {
	c.config = ctx.config
}

func (c *containerInformer) Start(stopCh <-chan struct{}) {
	klog.V(2).Infof("starting container informer")

	var err error
	var unixEndpoint string
	unixEndpoint, err = runtime.GetContainerdEndpoint()
	if err != nil {
		klog.Fatalf("failed to get containerd endpoint, error: %v", err)
	}

	c.runtimeServiceClient, err = handler.GetRuntimeClient(unixEndpoint)
	if err != nil {
		klog.Fatalf("failed to create runtime service client, %v", err)
	}

	hdlID := c.pleg.AddHandler(pleg.PodLifeCycleHandlerFuncs{
		ContainerAddedFunc: func(podID, containerID string) {
			if len(c.containerCreated) == 0 {
				c.containerCreated <- createEvent{podID: podID, containerID: containerID}
				klog.V(5).Infof("new container %v created, send event to sync containers", containerID)
			} else {
				klog.V(5).Infof("new container %v created, last event has not been consumed, no need to send event",
					podID)
			}
		}})
	defer c.pleg.RemoverHandler(hdlID)

	go c.syncCRILoop(c.config.CRISyncInterval, stopCh)
	go func() {
		if err := c.pleg.Run(stopCh); err != nil {
			klog.Fatalf("Unable to run the pleg: ", err)
		}
	}()

	klog.Infof("container informer started")
	<-stopCh
	klog.Infof("shutting down container informer daemon")
}

func (c *containerInformer) syncCRILoop(syncInterval time.Duration, stopChan <-chan struct{}) {
	timer := time.NewTimer(syncInterval)
	defer timer.Stop()

	c.syncContainers()
	rateLimiter := rate.NewLimiter(5, 10)
	for {
		select {
		case <-c.containerCreated:
			if rateLimiter.Allow() {
				klog.V(4).Infof("new container created, sync from CRI immediately")
				c.syncContainers()
			}
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(syncInterval)
		case <-timer.C:
			timer.Reset(syncInterval)
			c.syncContainers()
		case <-stopChan:
			klog.Infof("sync CRI loop is exited")
			return
		}
	}
}

func (c *containerInformer) syncContainers() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.config.CRISyncTimeout)
	defer cancel()

	listPodResp, err := c.runtimeServiceClient.ListPodSandbox(ctx, &runtimeapi.ListPodSandboxRequest{})
	if err != nil || len(listPodResp.Items) == 0 {
		klog.Warningf("list pod sandbox from CRI failed, err: %v", err)
		return err
	}
	listContainerReq := &runtimeapi.ListContainersRequest{}
	for _, podSandBox := range listPodResp.Items {

		listContainerReq.Filter = &runtimeapi.ContainerFilter{PodSandboxId: podSandBox.Id}
		resp, err := c.runtimeServiceClient.ListContainers(ctx, listContainerReq)
		if err != nil || len(resp.Containers) == 0 {
			klog.Warningf("get containers from CRI failed, err: %v", err)
			return err
		}
		podUID := podSandBox.Metadata.Uid

		var getContainerReq *runtimeapi.ContainerStatusRequest
		getContainerReq = &runtimeapi.ContainerStatusRequest{ContainerId: podSandBox.Id}
		statusResp, err := c.runtimeServiceClient.ContainerStatus(ctx, getContainerReq)
		if err != nil {
			klog.Warningf("get container status from CRI failed, err: %v", err)
			return err
		}
		c.sandboxContainerMap[podUID] = &ContainerMeta{
			Status: statusResp.Status,
			PodUID: podUID,
		}

		for _, container := range resp.Containers {
			getContainerReq = &runtimeapi.ContainerStatusRequest{ContainerId: container.Id}
			statusResp, err := c.runtimeServiceClient.ContainerStatus(ctx, getContainerReq)
			if err != nil {
				klog.Warningf("get container status from CRI failed, err: %v", err)
				return err
			}
			if _, ok := c.containerMap[podUID]; !ok {
				c.containerMap[podUID] = map[string]*ContainerMeta{}
			}
			c.containerMap[podUID][container.Id] = &ContainerMeta{
				Status: statusResp.Status,
				PodUID: podUID,
			}
		}
	}
	return nil
}

func (c *containerInformer) HasSynced() bool {
	synced := c.containerHasSynced.Load()
	klog.V(5).Infof("container informer has synced %v", synced)
	return synced
}

func (c *containerInformer) GetContainer(containerHashID string) *ContainerMeta {
	c.containerRWMutex.RLock()
	defer c.containerRWMutex.RUnlock()

	for _, containers := range c.containerMap {
		for cID, cMeta := range containers {
			if cID == containerHashID {
				return cMeta
			}
		}
	}
	return nil
}

func (c *containerInformer) GetAllSandboxContainers() []*ContainerMeta {
	c.containerRWMutex.RLock()
	defer c.containerRWMutex.RUnlock()

	ret := []*ContainerMeta{}
	for _, cMeta := range c.sandboxContainerMap {
		ret = append(ret, cMeta)
	}
	return ret
}

func (c *containerInformer) GetSandboxContainer(podUID string) *ContainerMeta {
	c.containerRWMutex.RLock()
	defer c.containerRWMutex.RUnlock()

	for pID, cMeta := range c.sandboxContainerMap {
		if pID == podUID {
			return cMeta
		}
	}
	return nil
}

func (c *containerInformer) GetContainersByPodUID(podUID string) []*ContainerMeta {
	c.containerRWMutex.RLock()
	defer c.containerRWMutex.RUnlock()

	ret := []*ContainerMeta{}
	for pID, containers := range c.containerMap {
		if pID == podUID {
			for _, cMeta := range containers {
				ret = append(ret, cMeta)
			}
			break
		}
	}
	return ret
}
