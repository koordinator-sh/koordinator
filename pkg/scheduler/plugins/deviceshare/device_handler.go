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

package deviceshare

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func (n *nodeDeviceCache) onDeviceAdd(obj interface{}) {
	device, ok := obj.(*schedulingv1alpha1.Device)
	if !ok {
		klog.Errorf("device cache add failed to parse, obj %T", obj)
		return
	}
	n.update(device.Name, device)
	klog.V(4).InfoS("device cache added", "Device", klog.KObj(device))
}

func (n *nodeDeviceCache) onDeviceUpdate(oldObj, newObj interface{}) {
	_, oldOK := oldObj.(*schedulingv1alpha1.Device)
	newD, newOK := newObj.(*schedulingv1alpha1.Device)
	if !oldOK || !newOK {
		klog.Errorf("device cache update failed to parse, oldObj %T, newObj %T", oldObj, newObj)
		return
	}
	n.update(newD.Name, newD)
	klog.V(4).InfoS("device cache updated", "Device", klog.KObj(newD))
}

func (n *nodeDeviceCache) onDeviceDelete(obj interface{}) {
	var device *schedulingv1alpha1.Device
	switch t := obj.(type) {
	case *schedulingv1alpha1.Device:
		device = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		device, ok = t.Obj.(*schedulingv1alpha1.Device)
		if !ok {
			return
		}
	default:
		return
	}
	n.remove(device.Name)
	klog.V(4).InfoS("device cache deleted", "Device", klog.KObj(device))
}

func (n *nodeDeviceCache) remove(nodeName string) {
	if nodeName == "" {
		return
	}
	n.removeNodeDevice(nodeName)
}

func (n *nodeDeviceCache) update(nodeName string, device *schedulingv1alpha1.Device) {
	if nodeName == "" || device == nil {
		return
	}

	info := n.getNodeDevice(nodeName)
	if info == nil {
		info = n.createNodeDevice(nodeName)
	}

	info.lock.Lock()
	defer info.lock.Unlock()

	nodeDeviceResource := map[schedulingv1alpha1.DeviceType]deviceResources{}
	for _, deviceInfo := range device.Spec.Devices {
		if nodeDeviceResource[deviceInfo.Type] == nil {
			nodeDeviceResource[deviceInfo.Type] = make(deviceResources)
		}
		if !deviceInfo.Health {
			nodeDeviceResource[deviceInfo.Type][int(*deviceInfo.Minor)] = make(v1.ResourceList)
			klog.Errorf("Find device unhealthy, nodeName:%v, deviceType:%v, minor:%v",
				nodeName, deviceInfo.Type, deviceInfo.Minor)
		} else {
			nodeDeviceResource[deviceInfo.Type][int(*deviceInfo.Minor)] = deviceInfo.Resources
			klog.V(5).Infof("Find device resource update, nodeName:%v, deviceType:%v, minor:%v, res:%v",
				nodeName, deviceInfo.Type, deviceInfo.Minor, deviceInfo.Resources)
		}
	}

	info.resetDeviceTotal(nodeDeviceResource)
}
