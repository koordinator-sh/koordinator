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

package impl

import (
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type updateCallback struct {
	name        string
	description string
	fn          statesinformer.UpdateCbFn
}

type UpdateCbCtx struct{}

type callbackRunner struct {
	callbackChans        map[statesinformer.RegisterType]chan UpdateCbCtx
	stateUpdateCallbacks map[statesinformer.RegisterType][]updateCallback
	statesInformer       statesinformer.StatesInformer
}

func NewCallbackRunner() *callbackRunner {
	c := &callbackRunner{}
	c.callbackChans = map[statesinformer.RegisterType]chan UpdateCbCtx{
		statesinformer.RegisterTypeNodeSLOSpec:  make(chan UpdateCbCtx, 1),
		statesinformer.RegisterTypeAllPods:      make(chan UpdateCbCtx, 1),
		statesinformer.RegisterTypeNodeTopology: make(chan UpdateCbCtx, 1),
		statesinformer.RegisterTypeNodeMetadata: make(chan UpdateCbCtx, 1),
	}
	c.stateUpdateCallbacks = map[statesinformer.RegisterType][]updateCallback{
		statesinformer.RegisterTypeNodeSLOSpec:  {},
		statesinformer.RegisterTypeAllPods:      {},
		statesinformer.RegisterTypeNodeTopology: {},
		statesinformer.RegisterTypeNodeMetadata: {},
	}
	return c
}

func (s *callbackRunner) Setup(i statesinformer.StatesInformer) {
	s.statesInformer = i
}

func (s *callbackRunner) RegisterCallbacks(rType statesinformer.RegisterType, name, description string, callbackFn statesinformer.UpdateCbFn) {
	callbacks, legal := s.stateUpdateCallbacks[rType]
	if !legal {
		klog.Fatalf("states informer callback register with type %v is illegal", rType.String())
	}
	for _, c := range callbacks {
		if c.name == name {
			klog.Fatalf("states informer callback register %s with type %v already registered", name, rType.String())
		}
	}
	newCb := updateCallback{
		name:        name,
		description: description,
		fn:          callbackFn,
	}
	s.stateUpdateCallbacks[rType] = append(s.stateUpdateCallbacks[rType], newCb)
	klog.V(1).Infof("states informer callback %s has registered for type %v", name, rType.String())
}

func (s *callbackRunner) SendCallback(objType statesinformer.RegisterType) {
	if _, exist := s.callbackChans[objType]; exist {
		select {
		case s.callbackChans[objType] <- struct{}{}:
			return
		default:
			klog.Infof("last callback runner %v has not finished, ignore this time", objType.String())
		}
	} else {
		klog.Warningf("callback runner %v is not exist", objType.String())
	}
}

func (s *callbackRunner) runCallbacks(objType statesinformer.RegisterType, obj interface{}) {
	callbacks, exist := s.stateUpdateCallbacks[objType]
	if !exist {
		klog.Errorf("states informer callbacks type %v not exist", objType.String())
		return
	}
	callbackTarget := &statesinformer.CallbackTarget{
		Pods: s.statesInformer.GetAllPods(),
	}
	if nodeSLO := s.statesInformer.GetNodeSLO(); nodeSLO != nil {
		callbackTarget.HostApplications = nodeSLO.Spec.HostApplications
	}
	for _, c := range callbacks {
		klog.V(5).Infof("start running callback function %v for type %v, pod num %v, host app num %v",
			c.name, objType.String(), len(callbackTarget.Pods), len(callbackTarget.HostApplications))
		c.fn(objType, obj, callbackTarget)
	}
}

func (s *callbackRunner) Start(stopCh <-chan struct{}) {
	for t := range s.callbackChans {
		cbType := t
		go func() {
			for {
				select {
				case cbCtx := <-s.callbackChans[cbType]:
					cbObj := s.getObjByType(cbType, cbCtx)
					if cbObj == nil {
						klog.Warningf("callback runner with type %T is not exist", cbObj)
					} else {
						s.runCallbacks(cbType, cbObj)
					}
				case <-stopCh:
					klog.Infof("callback runner %v loop is exited", cbType.String())
					return
				}
			}
		}()
	}
}

func (s *callbackRunner) getObjByType(objType statesinformer.RegisterType, cbCtx UpdateCbCtx) interface{} {
	switch objType {
	case statesinformer.RegisterTypeNodeSLOSpec:
		nodeSLO := s.statesInformer.GetNodeSLO()
		if nodeSLO != nil {
			return &nodeSLO.Spec
		}
		return nil
	case statesinformer.RegisterTypeAllPods:
		return &struct{}{}
	case statesinformer.RegisterTypeNodeTopology:
		return s.statesInformer.GetNodeTopo()
	case statesinformer.RegisterTypeNodeMetadata:
		return s.statesInformer.GetNode()
	}
	return nil
}
