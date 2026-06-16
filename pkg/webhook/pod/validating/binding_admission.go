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

package validating

import (
	"context"
	"flag"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	webhookmetrics "github.com/koordinator-sh/koordinator/pkg/webhook/metrics"
)

var (
	// BindingAdmissionUserName is a comma-separated allowlist of
	// UserInfo.Username substrings. Empty disables the check entirely.
	BindingAdmissionUserName = ""

	// BindingAdmissionDryRun enables dry-run mode: all requests are allowed,
	// but would-be denials are logged and recorded as metrics.
	BindingAdmissionDryRun = false

	// BindingAdmissionNamespaces is a comma-separated list of namespaces to
	// enforce. Empty means no enforcement (unless "*"). "*" means all namespaces.
	BindingAdmissionNamespaces = ""

	// BindingAdmissionExcludeNamespaces is a comma-separated list of namespaces
	// that are always allowed (e.g. kube-system to protect control-plane pods).
	BindingAdmissionExcludeNamespaces = "kube-system"

	// BindingAdmissionBypassLabels is a comma-separated list of pod label keys.
	// If a pod has ANY of these labels (regardless of value), the binding request
	// is allowed. Used to exempt pods produced by delegated schedulers (e.g. VK).
	BindingAdmissionBypassLabels = "koordinator.sh/binding-admission-bypass"

	// BindingAdmissionBypassAnnotations is a comma-separated list of pod
	// annotation keys. If a pod has ANY of these annotations (regardless of
	// value), the binding request is allowed. Used to exempt pods produced by
	// delegated schedulers that mark pods via annotations.
	BindingAdmissionBypassAnnotations = ""
)

var (
	allowedUserNames  []string
	includedNS        map[string]struct{} // nil when disabled or allNamespaces=true
	excludedNS        map[string]struct{}
	bypassLabels      map[string]struct{}
	bypassAnnotations map[string]struct{}
	allNamespaces     bool // true when BindingAdmissionNamespaces == "*"
)

// InitBindingAdmissionFlags registers all binding-admission flags.
func InitBindingAdmissionFlags(fs *flag.FlagSet) {
	fs.StringVar(&BindingAdmissionUserName, "binding-admission-user-name", BindingAdmissionUserName,
		"Comma-separated list of allowed UserInfo.Username substrings for pod binding admission. "+
			"Empty disables the check. Example: 'koord-scheduler,custom-scheduler'")
	fs.BoolVar(&BindingAdmissionDryRun, "binding-admission-dry-run", BindingAdmissionDryRun,
		"If true, binding admission runs in dry-run mode: all requests are allowed, "+
			"but would-be denials are logged and recorded as metrics.")
	fs.StringVar(&BindingAdmissionNamespaces, "binding-admission-namespaces", BindingAdmissionNamespaces,
		"Comma-separated list of namespaces to enforce binding admission. "+
			"Empty disables namespace filtering. '*' enforces on all namespaces.")
	fs.StringVar(&BindingAdmissionExcludeNamespaces, "binding-admission-exclude-namespaces",
		BindingAdmissionExcludeNamespaces,
		"Comma-separated list of namespaces always allowed (bypass binding admission). "+
			"Use to protect control-plane pods. Default: 'kube-system'")
	fs.StringVar(&BindingAdmissionBypassLabels, "binding-admission-bypass-labels",
		BindingAdmissionBypassLabels,
		"Comma-separated list of pod label keys. If a pod has any of these labels "+
			"(regardless of value), its binding request is allowed. "+
			"Default: 'koordinator.sh/binding-admission-bypass'")
	fs.StringVar(&BindingAdmissionBypassAnnotations, "binding-admission-bypass-annotations",
		BindingAdmissionBypassAnnotations,
		"Comma-separated list of pod annotation keys. If a pod has any of these "+
			"annotations (regardless of value), its binding request is allowed. "+
			"Empty disables annotation bypass.")
}

// SetupBindingAdmission parses all flag values into pre-computed structures.
// Must be called AFTER flag.Parse().
func SetupBindingAdmission() {
	allowedUserNames = parseList(BindingAdmissionUserName)
	if len(allowedUserNames) > 0 {
		klog.Infof("binding admission: user-name whitelist = %v", allowedUserNames)
	}

	if BindingAdmissionNamespaces == "*" {
		allNamespaces = true
		includedNS = nil
		klog.Infof("binding admission: enforcing on ALL namespaces")
	} else {
		allNamespaces = false
		includedNS = parseSet(BindingAdmissionNamespaces)
		if len(includedNS) > 0 {
			klog.Infof("binding admission: enforcing on namespaces = %v", mapKeys(includedNS))
		}
	}

	excludedNS = parseSet(BindingAdmissionExcludeNamespaces)
	if len(excludedNS) > 0 {
		klog.Infof("binding admission: excluding namespaces = %v", mapKeys(excludedNS))
	}

	bypassLabels = parseSet(BindingAdmissionBypassLabels)
	if len(bypassLabels) > 0 {
		klog.Infof("binding admission: bypass labels = %v", mapKeys(bypassLabels))
	}

	bypassAnnotations = parseSet(BindingAdmissionBypassAnnotations)
	if len(bypassAnnotations) > 0 {
		klog.Infof("binding admission: bypass annotations = %v", mapKeys(bypassAnnotations))
	}

	if BindingAdmissionDryRun {
		klog.Infof("binding admission: DRY-RUN mode enabled")
	}
}

// +kubebuilder:webhook:path=/validate-pod-binding,mutating=false,failurePolicy=ignore,sideEffects=None,admissionReviewVersions=v1;v1beta1,groups="",resources=pods/binding,verbs=create,versions=v1,name=vpodbinding.koordinator.sh

var _ admission.Handler = &BindingAdmissionHandler{}

// BindingAdmissionHandler intercepts pod binding requests to restrict which
// schedulers are allowed to bind pods. Used during scheduler migration to
// ensure legacy schedulers cannot bind pods that should be handled by
// koord-scheduler.
//
// Escape mechanisms (defense in depth):
//  1. FeatureGate: --feature-gates=BindingAdmissionWebhook=false → full bypass
//  2. Empty whitelist: --binding-admission-user-name="" → check disabled
//  3. Namespace exclusion: --binding-admission-exclude-namespaces=kube-system
//  4. Pod bypass label: --binding-admission-bypass-labels (configurable keys)
//  5. Pod bypass annotation: --binding-admission-bypass-annotations (configurable keys)
//  6. Dry-run mode: --binding-admission-dry-run=true → allow all, log denials
//  7. failurePolicy=Ignore: webhook unreachable → bindings proceed normally
type BindingAdmissionHandler struct {
	// Client is a cached reader backed by SharedInformer (zero API server cost).
	Client client.Reader
}

// Handle processes a binding admission request.
//
// Hot-path design for high QPS:
//   - Whitelist match: O(n) string contains on pre-parsed []string, n typically ≤ 3
//   - Namespace checks: O(1) map lookups
//   - Pod label/annotation (cache read): only when username mismatch, before deny
//   - Metrics: atomic counter increment
func (h *BindingAdmissionHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	// Escape 1: FeatureGate kill switch.
	if !utilfeature.DefaultFeatureGate.Enabled(features.BindingAdmissionWebhook) {
		return admission.Allowed("")
	}

	// Escape 2: empty whitelist disables the check.
	if len(allowedUserNames) == 0 {
		return admission.Allowed("")
	}

	// Only intercept pod binding create requests.
	if req.Resource.Resource != "pods" || req.SubResource != "binding" || req.Operation != admissionv1.Create {
		return admission.Allowed("")
	}

	podNS := req.Namespace
	podKey := types.NamespacedName{Namespace: podNS, Name: req.Name}
	username := req.UserInfo.Username

	// Check username whitelist (hot path: pre-parsed, O(n) string contains).
	for _, name := range allowedUserNames {
		if strings.Contains(username, name) {
			webhookmetrics.RecordBindingAdmissionDecision(webhookmetrics.DecisionAllowedByUserName)
			return admission.Allowed("")
		}
	}

	// --- Below: username not in whitelist, apply gray/scope checks before deny ---

	// Escape 3: Namespace exclusion — always allow (protects kube-system, etc.).
	if _, excluded := excludedNS[podNS]; excluded {
		klog.V(5).Infof("binding admission: pod %s in excluded namespace, allowing", podKey)
		webhookmetrics.RecordBindingAdmissionDecision(webhookmetrics.DecisionExcluded)
		return admission.Allowed("")
	}

	// Escape 3 (cont.): Namespace scope — only enforce if namespace is in scope.
	if !allNamespaces {
		if len(includedNS) == 0 {
			// No namespaces configured → no enforcement.
			return admission.Allowed("")
		}
		if _, inScope := includedNS[podNS]; !inScope {
			klog.V(5).Infof("binding admission: pod %s not in gray scope, allowing", podKey)
			webhookmetrics.RecordBindingAdmissionDecision(webhookmetrics.DecisionOutOfScope)
			return admission.Allowed("")
		}
	}

	// Escape 4/5: Pod bypass label/annotation (cache read from SharedInformer, zero API server cost).
	// Fail-open: if cache read fails, allow the binding to avoid blocking on transient errors.
	if h.Client != nil {
		pod := &corev1.Pod{}
		if err := h.Client.Get(ctx, podKey, pod); err != nil {
			klog.V(3).Infof("binding admission: failed to get pod %s from cache, fail-open: %v", podKey, err)
			return admission.Allowed("")
		}

		// Check configurable bypass labels (presence-based: key exists → bypass).
		for key := range bypassLabels {
			if _, ok := pod.Labels[key]; ok {
				klog.V(4).Infof("binding admission: pod %s has bypass label %q, allowing", podKey, key)
				webhookmetrics.RecordBindingAdmissionDecision(webhookmetrics.DecisionBypassLabel)
				return admission.Allowed("")
			}
		}

		// Check configurable bypass annotations (presence-based: key exists → bypass).
		for key := range bypassAnnotations {
			if _, ok := pod.Annotations[key]; ok {
				klog.V(4).Infof("binding admission: pod %s has bypass annotation %q, allowing", podKey, key)
				webhookmetrics.RecordBindingAdmissionDecision(webhookmetrics.DecisionBypassAnnotation)
				return admission.Allowed("")
			}
		}
	}

	// Escape 6: Would deny — check dry-run mode.
	if BindingAdmissionDryRun {
		klog.V(3).Infof("binding admission [DRY-RUN]: would deny pod %s, username=%q not in allowlist names=%v",
			podKey, username, allowedUserNames)
		webhookmetrics.RecordBindingAdmissionDecision(webhookmetrics.DecisionDryRun)
		return admission.Allowed("")
	}

	// Deny.
	klog.V(3).Infof("binding admission denied: pod %s, username=%q not in allowlist names=%v",
		podKey, username, allowedUserNames)
	webhookmetrics.RecordBindingAdmissionDecision(webhookmetrics.DecisionDenied)
	return admission.Denied("binding denied: username is not in the allowed list")
}

func parseList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func parseSet(s string) map[string]struct{} {
	items := parseList(s)
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(items))
	for _, item := range items {
		m[item] = struct{}{}
	}
	return m
}

func mapKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
