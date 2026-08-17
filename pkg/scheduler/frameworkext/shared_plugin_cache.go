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
//
// Start runs exactly once, on the ExtendedHandle of whichever profile first constructed the
// shared cache, so it may only use factory-scoped (instance-singleton) dependencies — the
// shared/koord informer factories, the scheduler cache, etc. Per-extender state must NOT be
// registered here: a ForgetPod handler, for example, is per-extender (see
// ExtendedHandle.RegisterForgetPodHandler), so registering it in Start would only wire the
// one profile's extender and miss ForgetPod invoked through any other profile. Register such
// per-profile hooks in the plugin's New() instead, where every profile runs.
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
// assumed allocations during the Reserve scheduling phase. Implementers uphold the
// assume/forget contract: AssumePod records what Reserve wrote as a rollback marker, and the
// marker is a pure rollback record — it is consumed ONLY by a negative event (ForgetPod, or a
// delete / bound→unassigned / terminate through OnPodDelete/OnPodUpdate), which rolls back the
// snapshot AssumePod recorded. A positive (assigned) event for an assumed pod must NOT re-add
// or reconcile it: spec.nodeName is not a trustworthy bind signal (multi-scheduler arbitration
// fakes it via a client-go transform and clears it back to ""), and the marker must survive
// for a racing ForgetPod on arbitration failure. Because the marker is not consumed on a
// positive event, it lives for the whole lifetime of a successfully-bound pod.
//
// Two consequences implementers must accept: (1) rollback is against the Reserve-time snapshot,
// not the pod's current annotation, so post-assume annotation changes are not reconciled while
// the marker is live; (2) if a pod is genuinely bound to a node other than the one Reserve
// assumed, that node is not credited until a later informer event arrives after the marker is
// cleared. This is a deliberate departure from an annotation-authoritative "reconcile on every
// event" model, chosen for arbitration safety.
type CacheReserver interface {
	SharedPluginCache
	AssumePod(pod *corev1.Pod, nodeName string) error
	ForgetPod(pod *corev1.Pod) error
}
