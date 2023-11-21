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

package v1alpha1

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	// batch resource can be shared with other allocators such as Hadoop YARN
	// record origin batch allocatable on node for calculating the batch allocatable of K8s and YARN, e.g.
	// k8s_batch_allocatable = origin_batch_allocatable - yarn_batch_requested
	// yarn_allocatable = origin_batch_allocatable - k8s_batch_requested
	NodeOriginExtendedAllocatableAnnotationKey = "node.koordinator.sh/originExtendedAllocatable"

	// record (batch) allocations of other schedulers such as YARN, which should be excluded before updating node extended resource
	NodeThirdPartyAllocationsAnnotationKey = "node.koordinator.sh/thirdPartyAllocations"
)

type OriginAllocatable struct {
	Resources corev1.ResourceList `json:"resources,omitempty"`
}

func GetOriginExtendedAllocatable(annotations map[string]string) (*OriginAllocatable, error) {
	originAllocatableStr, exist := annotations[NodeOriginExtendedAllocatableAnnotationKey]
	if !exist {
		return nil, nil
	}
	originAllocatable := &OriginAllocatable{}
	if err := json.Unmarshal([]byte(originAllocatableStr), originAllocatable); err != nil {
		return nil, err
	}
	return originAllocatable, nil
}

func SetOriginExtendedAllocatableRes(annotations map[string]string, extendedAllocatable corev1.ResourceList) error {
	old, err := GetOriginExtendedAllocatable(annotations)
	if old == nil || err != nil {
		old = &OriginAllocatable{}
	}
	if old.Resources == nil {
		old.Resources = map[corev1.ResourceName]resource.Quantity{}
	}
	for resourceName, value := range extendedAllocatable {
		old.Resources[resourceName] = value
	}
	newStr, err := json.Marshal(old)
	if err != nil {
		return err
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[NodeOriginExtendedAllocatableAnnotationKey] = string(newStr)
	return nil
}

type ThirdPartyAllocations struct {
	Allocations []ThirdPartyAllocation `json:"allocations,omitempty"`
}

type ThirdPartyAllocation struct {
	Name      string               `json:"name"`
	Priority  apiext.PriorityClass `json:"priority"`
	Resources corev1.ResourceList  `json:"resources,omitempty"`
}

func GetThirdPartyAllocations(annotations map[string]string) (*ThirdPartyAllocations, error) {
	valueStr, exist := annotations[NodeThirdPartyAllocationsAnnotationKey]
	if !exist {
		return nil, nil
	}
	object := &ThirdPartyAllocations{}
	if err := json.Unmarshal([]byte(valueStr), object); err != nil {
		return nil, err
	}
	return object, nil
}

func GetThirdPartyAllocatedResByPriority(annotations map[string]string, priority apiext.PriorityClass) (corev1.ResourceList, error) {
	allocations, err := GetThirdPartyAllocations(annotations)
	if err != nil || allocations == nil {
		return nil, err
	}
	result := corev1.ResourceList{}
	for _, alloc := range allocations.Allocations {
		if alloc.Priority == priority {
			result = quotav1.Add(result, alloc.Resources)
		}
	}
	return result, nil
}

func SetThirdPartyAllocation(annotations map[string]string, name string, priority apiext.PriorityClass,
	resource corev1.ResourceList) error {
	// parse or init old allocations
	oldAllocations, err := GetThirdPartyAllocations(annotations)
	if oldAllocations == nil || err != nil {
		oldAllocations = &ThirdPartyAllocations{}
	}
	if oldAllocations.Allocations == nil {
		oldAllocations.Allocations = make([]ThirdPartyAllocation, 0, 1)
	}

	// create or update old alloc
	newAlloc := ThirdPartyAllocation{
		Name:      name,
		Priority:  priority,
		Resources: resource,
	}
	exist := false
	for i := range oldAllocations.Allocations {
		if oldAllocations.Allocations[i].Name == name {
			oldAllocations.Allocations[i] = newAlloc
			exist = true
			break
		}
	}
	if !exist {
		oldAllocations.Allocations = append(oldAllocations.Allocations, newAlloc)
	}

	// update allocation string
	newStr, err := json.Marshal(oldAllocations)
	if err != nil {
		return err
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[NodeThirdPartyAllocationsAnnotationKey] = string(newStr)
	return nil
}
