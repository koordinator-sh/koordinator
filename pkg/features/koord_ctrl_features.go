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

package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// NodeMetricControl is responsible for NodeMetric CR reconciliation
	NodeMetricControl featuregate.Feature = "NodeMetricControl"
	// NodeResourceControl is responsible for node BE allocatable resource calculation and reporting
	NodeResourceControl featuregate.Feature = "NodeResourceControl"
)

func init() {
	runtime.Must(defaultKoordCtrlMutableFeatureGate.Add(defaultKoordCtrlFeatureGates))
}

var (
	defaultKoordCtrlMutableFeatureGate featuregate.MutableFeatureGate = featuregate.NewFeatureGate()
	DefaultKoordCtlFeatureGate         featuregate.FeatureGate        = defaultKoordCtrlMutableFeatureGate

	defaultKoordCtrlFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
		NodeMetricControl:   {Default: true, PreRelease: featuregate.Beta},
		NodeResourceControl: {Default: false, PreRelease: featuregate.Alpha},
	}
)
