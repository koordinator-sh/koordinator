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

package resource_executor

import (
	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/runtime-manager/resource-executor/cri"
	"github.com/koordinator-sh/koordinator/pkg/runtime-manager/store"
)

type RuntimeResourceExecutor interface {
	GetMetaInfo() string
	GenerateResourceCheckpoint() interface{}
	GenerateHookRequest() interface{}
	ParseRequest(request interface{}) error
	UpdateResource(response interface{}) error
	ResourceCheckPoint(response interface{}) error
	DeleteCheckpointIfNeed(request interface{}) error
}

type RuntimeResourceType string

const (
	RuntimePodResource       RuntimeResourceType = "RuntimePodResource"
	RuntimeContainerResource RuntimeResourceType = "RuntimeContainerResource"
	RuntimeNoopResource      RuntimeResourceType = "RuntimeNoopResource"
)

func NewRuntimeResourceExecutor(runtimeResourceType RuntimeResourceType, store *store.MetaManager) RuntimeResourceExecutor {
	switch runtimeResourceType {
	case RuntimePodResource:
		return cri.NewPodResourceExecutor(store)
	case RuntimeContainerResource:
		return cri.NewContainerResourceExecutor(store)
	}
	return &NoopResourceExecutor{}
}

// NoopResourceExecutor means no-operation for cri request,
// where no hook exists like ListContainerStats/ExecSync etc.
type NoopResourceExecutor struct {
}

func (n *NoopResourceExecutor) GetMetaInfo() string {
	return ""
}

func (n *NoopResourceExecutor) GenerateResourceCheckpoint() interface{} {
	return v1alpha1.ContainerResourceHookRequest{}
}

func (n *NoopResourceExecutor) GenerateHookRequest() interface{} {
	return store.ContainerInfo{}
}

func (n *NoopResourceExecutor) ParseRequest(request interface{}) error {
	return nil
}
func (n *NoopResourceExecutor) ResourceCheckPoint(response interface{}) error {
	return nil
}

func (n *NoopResourceExecutor) DeleteCheckpointIfNeed(request interface{}) error {
	return nil
}

func (n *NoopResourceExecutor) UpdateResource(response interface{}) error {
	return nil
}
