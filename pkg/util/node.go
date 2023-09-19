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

package util

import (
	"encoding/json"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

// GenerateNodeKey returns a generated key with given meta
func GenerateNodeKey(node *metav1.ObjectMeta) string {
	return fmt.Sprintf("%v/%v", node.GetNamespace(), node.GetName())
}

// GetNodeAddress get node specified type address.
func GetNodeAddress(node *corev1.Node, addrType corev1.NodeAddressType) (string, error) {
	for _, address := range node.Status.Addresses {
		if address.Type == addrType {
			return address.Address, nil
		}
	}
	return "", fmt.Errorf("no address matched types %v", addrType)
}

// IsNodeAddressTypeSupported determine whether addrType is a supported type.
func IsNodeAddressTypeSupported(addrType corev1.NodeAddressType) bool {
	if addrType == corev1.NodeHostName ||
		addrType == corev1.NodeExternalIP ||
		addrType == corev1.NodeExternalDNS ||
		addrType == corev1.NodeInternalIP ||
		addrType == corev1.NodeInternalDNS {
		return true
	}
	return false
}

func GetNodeReservationFromKubelet(node *corev1.Node) corev1.ResourceList {
	if node == nil {
		return corev1.ResourceList{}
	}

	capacity := corev1.ResourceList{}
	if cpu, exist := node.Status.Capacity[corev1.ResourceCPU]; exist {
		capacity[corev1.ResourceCPU] = cpu
	}
	if memory, exist := node.Status.Capacity[corev1.ResourceMemory]; exist {
		capacity[corev1.ResourceMemory] = memory
	}

	allocatable := corev1.ResourceList{}
	if cpu, exist := node.Status.Allocatable[corev1.ResourceCPU]; exist {
		allocatable[corev1.ResourceCPU] = cpu
	}
	if memory, exist := node.Status.Allocatable[corev1.ResourceMemory]; exist {
		allocatable[corev1.ResourceMemory] = memory
	}

	return quotav1.Max(quotav1.Subtract(capacity, allocatable), NewZeroResourceList())
}

func GetNodeReservationFromAnnotation(anno map[string]string) corev1.ResourceList {
	reservation, err := apiext.GetNodeReservation(anno)
	if err != nil {
		klog.ErrorS(err, "Failed to apiext.GetNodeReservation")
		return nil
	}
	if reservation == nil {
		return nil
	}
	resources, err := GetNodeReservationResources(reservation)
	if err != nil {
		klog.ErrorS(err, "Failed to GetNodeReservationResources")
		return nil
	}
	return resources
}

func GetNodeReservationResources(reservation *apiext.NodeReservation) (corev1.ResourceList, error) {
	var resourceList corev1.ResourceList
	if reservation.Resources != nil {
		resourceList = reservation.Resources
	}
	if reservation.ReservedCPUs != "" {
		cpus, err := cpuset.Parse(reservation.ReservedCPUs)
		if err != nil {
			return nil, err
		}
		if resourceList == nil {
			resourceList = make(corev1.ResourceList)
		}
		resourceList[corev1.ResourceCPU] = resource.MustParse(strconv.Itoa(cpus.Size()))
	}

	return resourceList, nil
}

func TrimNodeAllocatableByNodeReservation(node *corev1.Node) (trimmedAllocatable corev1.ResourceList, trimmed bool) {
	trimmedAllocatable = node.Status.Allocatable
	reservation, err := apiext.GetNodeReservation(node.Annotations)
	if err != nil {
		return
	}
	if reservation == nil {
		return
	}
	shouldTrim := reservation.ApplyPolicy == "" || reservation.ApplyPolicy == apiext.NodeReservationApplyPolicyDefault
	if !shouldTrim {
		return
	}

	reservedResources, err := GetNodeReservationResources(reservation)
	if err != nil {
		return
	}
	if quotav1.IsZero(reservedResources) {
		return
	}

	allocatable := node.Status.Allocatable
	trimmedAllocatable = quotav1.SubtractWithNonNegativeResult(allocatable, reservedResources)

	// node.alloc(batch-memory) and node.alloc(batch-memory) have subtracted the reserved resources from the koord-manager,
	// so we should keep the original data here.
	trimmedAllocatable[apiext.BatchMemory] = allocatable[apiext.BatchMemory]
	trimmedAllocatable[apiext.BatchCPU] = allocatable[apiext.BatchCPU]
	trimmed = !quotav1.Equals(allocatable, trimmedAllocatable)
	return
}

func GetNodeAnnoReservedJson(reserved apiext.NodeReservation) string {
	result := ""
	resultBytes, err := json.Marshal(&reserved)
	if err == nil {
		result = string(resultBytes)
	}

	return result
}
