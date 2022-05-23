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

package statesinformer

import (
	"reflect"

	"k8s.io/klog/v2"
)

type updateCallback struct {
	name        string
	description string
	fn          UpdateCbFn
}

type UpdateCbFn func(s StatesInformer, statesObj interface{})

func (s *statesInformer) RegisterCallbacks(objType reflect.Type, name, description string, callbackFn UpdateCbFn) {
	callbacks, legal := s.stateUpdateCallbacks[objType]
	if !legal {
		klog.Fatalf("states informer callback register with type %v is illegal", objType)
	}
	for _, c := range callbacks {
		if c.name == name {
			klog.Fatalf("states informer callback register %s with type %v already registered", name, objType)
		}
	}
	newCb := updateCallback{
		name:        name,
		description: description,
		fn:          callbackFn,
	}
	s.stateUpdateCallbacks[objType] = append(s.stateUpdateCallbacks[objType], newCb)
	klog.Infof("states informer callback %s has registered", name)
}

func (s *statesInformer) runCallbacks(objType reflect.Type, obj interface{}) {
	callbacks, exist := s.stateUpdateCallbacks[objType]
	if !exist {
		klog.Errorf("states informer callbacks type %v not exist", objType)
		return
	}
	for _, c := range callbacks {
		klog.V(5).Infof("start running callback function %v for type %v", c.name, objType)
		c.fn(s, obj)
	}
}
