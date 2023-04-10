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

package sloconfig

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type virtualNode = map[string]string

/** generateNodesByNodeSelector : generate testNodes to detect overlap for nodeConfigs */
func generateNodesByNodeSelector(nodeSelector *metav1.LabelSelector) []virtualNode {

	if nodeSelector == nil {
		return nil
	}

	virtualNodeNums := 1
	for _, e := range nodeSelector.MatchExpressions {
		valuesNum := len(e.Values)
		if valuesNum > 0 {
			virtualNodeNums = virtualNodeNums * valuesNum
		}
	}

	virtualNodes := make([]virtualNode, virtualNodeNums)

	for key, value := range nodeSelector.MatchLabels {
		for i := range virtualNodes {
			if virtualNodes[i] == nil {
				virtualNodes[i] = map[string]string{}
			}
			virtualNodes[i][key] = value
		}
	}

	for _, e := range nodeSelector.MatchExpressions {
		valuesNum := len(e.Values)
		if valuesNum > 0 {
			for i := range virtualNodes {
				if virtualNodes[i] == nil {
					virtualNodes[i] = map[string]string{}
				}
				virtualNodes[i][e.Key] = e.Values[i%valuesNum]
			}
		}
	}
	return virtualNodes
}
