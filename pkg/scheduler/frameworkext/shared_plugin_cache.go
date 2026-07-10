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

package frameworkext

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

// SharedPluginCache is implemented by a plugin's cache object when the cache should be
// shared as a single instance across all scheduler profiles. The framework extender
// manages its lifecycle via StartSharedCaches and routes pod/node events to it centrally
// through the unified dispatcher on FrameworkExtenderFactory. Plugins that need
// Koordinator-specific CRD events (e.g. DeviceShare's Device CRD) register those handlers
// themselves inside Start, since those events are plugin-specific.
type SharedPluginCache interface {
	Start(ctx context.Context)

	OnPodAdd(pod *corev1.Pod)
	OnPodUpdate(oldPod, newPod *corev1.Pod)
	OnPodDelete(pod *corev1.Pod)

	OnNodeAdd(node *corev1.Node)
	OnNodeUpdate(oldNode, newNode *corev1.Node)
	OnNodeDelete(node *corev1.Node)
}

// CacheReserver is an optional interface for SharedPluginCache implementations that write
// assumed allocations during the Reserve scheduling phase. Implementers must uphold the
// assume/forget contract: AssumePod records what Reserve wrote, ForgetPod (or the
// reconcile path in OnPodAdd/Update/Delete) rolls it back. The informer event is always
// authoritative — implementations reconcile rather than skip when it arrives for an
// assumed pod, so the cache converges on the event's state even when the event's pod
// object differs from what Reserve assumed.
type CacheReserver interface {
	SharedPluginCache
	AssumePod(pod *corev1.Pod, nodeName string) error
	ForgetPod(pod *corev1.Pod) error
}
