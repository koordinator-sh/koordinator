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

package transformer

import (
	"k8s.io/client-go/tools/cache"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

var deviceTransformers = []func(device *schedulingv1alpha1.Device){
	TransformDeviceWithDeprecatedResources,
}

func TransformDevice(obj interface{}) (interface{}, error) {
	var device *schedulingv1alpha1.Device
	switch t := obj.(type) {
	case *schedulingv1alpha1.Device:
		device = t
	case cache.DeletedFinalStateUnknown:
		device, _ = t.Obj.(*schedulingv1alpha1.Device)
	}
	if device == nil {
		return obj, nil
	}

	for _, fn := range deviceTransformers {
		fn(device)
	}

	if unknown, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		unknown.Obj = device
		return unknown, nil
	}
	return device, nil
}

func TransformDeviceWithDeprecatedResources(device *schedulingv1alpha1.Device) {
	for i := range device.Spec.Devices {
		deviceInfo := &device.Spec.Devices[i]
		replaceAndEraseWithResourcesMapper(deviceInfo.Resources, apiext.DeprecatedDeviceResourcesMapper)
	}
}
