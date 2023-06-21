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
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
)

const (
	// owner: @joseph
	// alpha: v0.1
	//
	// CompatibleCSIStorageCapacity is used to set a custom CSIStorageCapacity informer to
	// be compatible with clusters that do not support v1.CSIStorageCapacity.
	// The k8s v1.22 version needs to enable the FeatureGate
	CompatibleCSIStorageCapacity featuregate.Feature = "CompatibleCSIStorageCapacity"

	// owner: @joseph
	// alpha: v0.1
	//
	// DisableCSIStorageCapacityInformer is used to disable CSIStorageCapacity informer
	// Versions below k8s v1.22 need to enable the FeatureGate
	DisableCSIStorageCapacityInformer featuregate.Feature = "DisableCSIStorageCapacityInformer"
)

var defaultSchedulerFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	CompatibleCSIStorageCapacity:      {Default: false, PreRelease: featuregate.Alpha},
	DisableCSIStorageCapacityInformer: {Default: false, PreRelease: featuregate.Alpha},
}

func init() {
	runtime.Must(k8sfeature.DefaultMutableFeatureGate.Add(defaultSchedulerFeatureGates))
}
