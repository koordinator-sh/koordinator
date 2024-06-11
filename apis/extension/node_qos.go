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
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// AnnotationNodeBandwidth specifies the total network bandwidth of the node, which can
	// be set by cluster administrator or third party components. The value should be a valid
	// resource.Quantity. Unit: bps.
	AnnotationNodeBandwidth = NodeDomainPrefix + "/network-bandwidth"
)

func GetNodeTotalBandwidth(annotations map[string]string) (*resource.Quantity, error) {
	var (
		val string
		ok  bool
	)
	if val, ok = annotations[AnnotationNodeBandwidth]; !ok {
		return nil, nil
	}
	if quantity, err := resource.ParseQuantity(val); err != nil {
		return nil, fmt.Errorf("failed to parse node bandwidth %v", err)
	} else {
		return &quantity, nil
	}
}
