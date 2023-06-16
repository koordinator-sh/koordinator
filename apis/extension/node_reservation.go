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

package extension

import (
	"encoding/json"
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

const (
	AnnotationNodeReservation = NodeDomainPrefix + "/reservation"
)

// NodeReservation resource reserved by node.annotation,
// If node.annotation declares the resources to be reserved, like this:
//
//	 annotations:
//	   node.koordinator.sh/reservation: >-
//		    {"reservedCPUs":"0-5"}
type NodeReservation struct {
	// resources need to be reserved. like, {"cpu":"1C", "memory":"2Gi"}
	Resources corev1.ResourceList `json:"resources,omitempty"`
	// reserved cpus need to be reserved, such as 1-6, or 2,4,6,8
	ReservedCPUs string `json:"reservedCPUs,omitempty"`
	// ApplyPolicy indicates how the reserved resources take effect.
	ApplyPolicy NodeReservationApplyPolicy `json:"applyPolicy,omitempty"`
}

type NodeReservationApplyPolicy string

const (
	// NodeReservationApplyPolicyDefault will affect the total amount of schedulable resources of the node and reserve CPU Cores.
	// For example, NodeInfo.Allocatable will be modified in the scheduler to deduct the amount of reserved resources
	NodeReservationApplyPolicyDefault NodeReservationApplyPolicy = "Default"
	// NodeReservationApplyPolicyReservedCPUsOnly means that only CPU Cores are reserved, but it will
	// not affect the total amount of schedulable resources of the node.
	// The total amount of schedulable resources is taken into effect by the kubelet's reservation mechanism.
	// But koordinator need to exclude reserved CPUs when allocating CPU Cores
	NodeReservationApplyPolicyReservedCPUsOnly NodeReservationApplyPolicy = "ReservedCPUsOnly"
)

func GetNodeReservation(annotations map[string]string) (*NodeReservation, error) {
	if s := annotations[AnnotationNodeReservation]; s != "" {
		reservation := &NodeReservation{}
		if err := json.Unmarshal([]byte(s), &reservation); err != nil {
			return nil, err
		}
		return reservation, nil
	}
	return nil, nil
}

func GetReservedCPUs(annotations map[string]string) (reservedCPUs string, numReservedCPUs int) {
	reservation, err := GetNodeReservation(annotations)
	if err != nil {
		klog.ErrorS(err, "failed to GetNodeReservation")
		return
	}
	if reservation == nil {
		return
	}

	quantity := reservation.Resources[corev1.ResourceCPU]
	if quantity.MilliValue() > 0 {
		numReservedCPUs = int(math.Ceil(float64(quantity.MilliValue()) / 1000))
	}

	if reservation.ReservedCPUs != "" {
		numReservedCPUs = 0
	}
	reservedCPUs = reservation.ReservedCPUs
	return
}
