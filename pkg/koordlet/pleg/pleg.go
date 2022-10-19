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

package pleg

import (
	"errors"
	"path"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

const (
	eventsChanCapacity = 128
)

type PodLifeCycleHandler interface {
	OnPodAdded(podID string)
	OnPodDeleted(podID string)
	OnContainerAdded(podID, containerID string)
	OnContainerDeleted(podID, containerID string)
}

type PodLifeCycleHandlerFuncs struct {
	PodAddedFunc         func(podID string)
	PodDeletedFunc       func(podID string)
	ContainerAddedFunc   func(podID, containerID string)
	ContainerDeletedFunc func(podID, containerID string)
}

func (r PodLifeCycleHandlerFuncs) OnPodAdded(podID string) {
	if r.PodAddedFunc != nil {
		r.PodAddedFunc(podID)
	}
}

func (r PodLifeCycleHandlerFuncs) OnPodDeleted(podID string) {
	if r.PodDeletedFunc != nil {
		r.PodDeletedFunc(podID)
	}
}

func (r PodLifeCycleHandlerFuncs) OnContainerAdded(podID, containerID string) {
	if r.ContainerAddedFunc != nil {
		r.ContainerAddedFunc(podID, containerID)
	}
}

func (r PodLifeCycleHandlerFuncs) OnContainerDeleted(podID, containerID string) {
	if r.ContainerDeletedFunc != nil {
		r.ContainerDeletedFunc(podID, containerID)
	}
}

type HandlerID uint32

type Pleg interface {
	Run(<-chan struct{}) error
	AddHandler(PodLifeCycleHandler) HandlerID
	RemoverHandler(id HandlerID) PodLifeCycleHandler
}

func NewPLEG(cgroupRootPath string) (Pleg, error) {
	podWatcher, err := NewWatcher()
	if err != nil && !errors.Is(err, errNotSupported) {
		klog.Error("failed to create pod watcher", err)
		return nil, err
	}
	containerWatcher, err := NewWatcher()
	if err != nil && !errors.Is(err, errNotSupported) {
		klog.Error("failed to create container watcher", err)
		return nil, err
	}

	p := &pleg{
		cgroupRootPath: cgroupRootPath,
		idGenerator:    0,
		handlers:       make(map[HandlerID]PodLifeCycleHandler),

		podWatcher:       podWatcher,
		containerWatcher: containerWatcher,
		events:           make(chan *event, eventsChanCapacity),
	}

	return p, nil
}

type pleg struct {
	cgroupRootPath string

	// internal status of pleg
	idGenerator HandlerID

	handlerMutex sync.Mutex
	handlers     map[HandlerID]PodLifeCycleHandler

	podWatcher       Watcher
	containerWatcher Watcher

	events chan *event
}

func (p *pleg) AddHandler(handler PodLifeCycleHandler) HandlerID {
	p.handlerMutex.Lock()
	defer p.handlerMutex.Unlock()
	id := p.idGenerator
	p.handlers[id] = handler
	p.idGenerator++
	return id
}

func (p *pleg) RemoverHandler(id HandlerID) PodLifeCycleHandler {
	p.handlerMutex.Lock()
	defer p.handlerMutex.Unlock()
	handler := p.handlers[id]
	delete(p.handlers, id)
	return handler
}

func (p *pleg) Run(stopCh <-chan struct{}) error {
	if p.podWatcher == nil || p.containerWatcher == nil {
		klog.Errorf("podWatcher or containerWatcher failed to init, skip running pleg")
		return nil
	}
	qosClasses := []corev1.PodQOSClass{corev1.PodQOSGuaranteed, corev1.PodQOSBurstable, corev1.PodQOSBestEffort}
	for _, qosClass := range qosClasses {
		// here we choose cpu subsystem as ground truth,
		// since we only need to watch one of all subsystems, and cpu subsystem always and must exist
		cgroupPath := path.Join(p.cgroupRootPath, system.CgroupCPUDir, util.GetPodQoSRelativePath(qosClass))
		err := p.podWatcher.AddWatch(cgroupPath)
		if err != nil {
			klog.Errorf("failed to watch path %v err %v", cgroupPath, err)
			return err
		}
		defer p.podWatcher.RemoveWatch(cgroupPath)
	}

	go p.runEventHandler(stopCh)

	for {
		select {
		case evt := <-p.podWatcher.Event():
			switch TypeOf(evt) {
			case DirCreated:
				basename := path.Base(evt.Name)
				podID, err := util.ParsePodID(basename)
				if err != nil {
					klog.Infof("skip %v added event which is not a pod", evt.Name)
					continue
				}
				// handle Pod event
				p.events <- newPodEvent(podID, podAdded)
				// register watcher for containers
				p.containerWatcher.AddWatch(evt.Name)
			case DirRemoved:
				basename := path.Base(evt.Name)
				podID, err := util.ParsePodID(basename)
				if err != nil {
					klog.Infof("skip %v removed event which is not a pod", evt.Name)
					continue
				}
				// handle Pod event
				p.events <- newPodEvent(podID, podDeleted)
				// remove watcher for containers
				p.containerWatcher.RemoveWatch(evt.Name)
			default:
				klog.V(5).Infof("skip %v unknown event", evt.Name)
			}
		case err := <-p.podWatcher.Error():
			klog.Errorf("read pods event error: %v", err)
		case evt := <-p.containerWatcher.Event():
			switch TypeOf(evt) {
			case DirCreated:
				containerBasename := path.Base(evt.Name)
				containerID, containerErr := util.ParseContainerID(containerBasename)
				podBasename := path.Base(path.Dir(evt.Name))
				podID, podErr := util.ParsePodID(podBasename)
				if podErr != nil || containerErr != nil {
					klog.Infof("skip %v added event which is not a container", evt.Name)
					continue
				}
				// handle Container event
				p.events <- newContainerEvent(podID, containerID, containerAdded)
			case DirRemoved:
				containerBasename := path.Base(evt.Name)
				containerID, containerErr := util.ParseContainerID(containerBasename)
				podBasename := path.Base(path.Dir(evt.Name))
				podID, podErr := util.ParsePodID(podBasename)
				if podErr != nil || containerErr != nil {
					klog.Infof("skip %v removed event which is not a container", evt.Name)
					continue
				}
				// handle Container event
				p.events <- newContainerEvent(podID, containerID, containerDeleted)
			default:
				klog.V(5).Infof("skip %v unknown event", evt.Name)
			}
		case err := <-p.containerWatcher.Error():
			klog.Errorf("read containers event error: %v", err)
		case <-stopCh:
			return nil
		}
	}
}

func (p *pleg) runEventHandler(stopCh <-chan struct{}) {
	for {
		select {
		case evt := <-p.events:
			p.handleEvent(evt)
		case <-stopCh:
			return
		}
	}
}

func (p *pleg) handleEvent(event *event) {
	if event == nil {
		return
	}
	p.handlerMutex.Lock()
	defer p.handlerMutex.Unlock()
	for _, hdl := range p.handlers {
		switch event.eventType {
		case podAdded:
			hdl.OnPodAdded(event.podID)
		case podDeleted:
			hdl.OnPodDeleted(event.podID)
		case containerAdded:
			hdl.OnContainerAdded(event.podID, event.containerID)
		case containerDeleted:
			hdl.OnContainerDeleted(event.podID, event.containerID)
		}
	}
}

type internalEventType int

const (
	podAdded internalEventType = iota
	podDeleted
	containerAdded
	containerDeleted
)

type event struct {
	eventType   internalEventType
	podID       string
	containerID string
}

func newPodEvent(podID string, eventType internalEventType) *event {
	return &event{eventType: eventType, podID: podID}
}

func newContainerEvent(podID, containerID string, eventType internalEventType) *event {
	return &event{eventType: eventType, podID: podID, containerID: containerID}
}
