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
	"reflect"
	"sort"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

// saveState / restoreState helpers to snapshot all package-level vars.
type admissionState struct {
	userName        string
	dryRun          bool
	namespaces      string
	excludeNS       string
	bypassLabelsStr string
	bypassAnnotStr  string
	allowedNames    []string
	included        map[string]struct{}
	excluded        map[string]struct{}
	bLabels         map[string]struct{}
	bAnnotations    map[string]struct{}
	allNS           bool
}

func save() admissionState {
	return admissionState{
		userName: BindingAdmissionUserName, dryRun: BindingAdmissionDryRun,
		namespaces: BindingAdmissionNamespaces, excludeNS: BindingAdmissionExcludeNamespaces,
		bypassLabelsStr: BindingAdmissionBypassLabels, bypassAnnotStr: BindingAdmissionBypassAnnotations,
		allowedNames: allowedUserNames, included: includedNS, excluded: excludedNS,
		bLabels: bypassLabels, bAnnotations: bypassAnnotations, allNS: allNamespaces,
	}
}

func restore(s admissionState) {
	BindingAdmissionUserName = s.userName
	BindingAdmissionDryRun = s.dryRun
	BindingAdmissionNamespaces = s.namespaces
	BindingAdmissionExcludeNamespaces = s.excludeNS
	BindingAdmissionBypassLabels = s.bypassLabelsStr
	BindingAdmissionBypassAnnotations = s.bypassAnnotStr
	allowedUserNames = s.allowedNames
	includedNS = s.included
	excludedNS = s.excluded
	bypassLabels = s.bLabels
	bypassAnnotations = s.bAnnotations
	allNamespaces = s.allNS
}

func TestSetupBindingAdmission(t *testing.T) {
	tests := []struct {
		name         string
		userName     string
		ns           string
		excludeNS    string
		bypassLabels string
		bypassAnnot  string
		dryRun       bool
		wantNames    []string
		wantBypassL  []string
		wantBypassA  []string
		wantAllNS    bool
	}{
		{name: "empty all", userName: "", ns: "", excludeNS: "", wantNames: nil, wantBypassL: nil, wantBypassA: nil, wantAllNS: false},
		{name: "single name", userName: "koord-scheduler", ns: "", excludeNS: "", wantNames: []string{"koord-scheduler"}, wantBypassL: nil, wantBypassA: nil},
		{name: "multi names", userName: "a,b,c", ns: "", excludeNS: "", wantNames: []string{"a", "b", "c"}, wantBypassL: nil, wantBypassA: nil},
		{name: "name trim spaces", userName: " a , b ", ns: "", excludeNS: "", wantNames: []string{"a", "b"}, wantBypassL: nil, wantBypassA: nil},
		{name: "star = all namespaces", userName: "x", ns: "*", excludeNS: "", wantNames: []string{"x"}, wantBypassL: nil, wantBypassA: nil, wantAllNS: true},
		{name: "bypass labels configured", userName: "koord", bypassLabels: "app.kubernetes.io/managed-by,custom-label",
			wantNames: []string{"koord"}, wantBypassL: []string{"app.kubernetes.io/managed-by", "custom-label"}, wantBypassA: nil},
		{name: "bypass annotations configured", userName: "koord", bypassAnnot: "custom.io/delegated",
			wantNames: []string{"koord"}, wantBypassL: nil, wantBypassA: []string{"custom.io/delegated"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := save()
			defer restore(s)

			BindingAdmissionUserName = tt.userName
			BindingAdmissionNamespaces = tt.ns
			BindingAdmissionExcludeNamespaces = tt.excludeNS
			BindingAdmissionBypassLabels = tt.bypassLabels
			BindingAdmissionBypassAnnotations = tt.bypassAnnot
			BindingAdmissionDryRun = tt.dryRun
			SetupBindingAdmission()

			if tt.wantNames == nil && allowedUserNames != nil {
				t.Errorf("expected nil user names, got %v", allowedUserNames)
			}
			if tt.wantNames != nil && !reflect.DeepEqual(allowedUserNames, tt.wantNames) {
				t.Errorf("expected user names %v, got %v", tt.wantNames, allowedUserNames)
			}
			if tt.wantBypassL == nil && bypassLabels != nil {
				t.Errorf("expected nil bypass labels, got %v", bypassLabels)
			}
			if tt.wantBypassL != nil {
				got := mapKeys(bypassLabels)
				sort.Strings(got)
				want := append([]string(nil), tt.wantBypassL...)
				sort.Strings(want)
				if len(got) != len(want) {
					t.Errorf("expected bypass labels %v, got %v", want, got)
				} else {
					for i := range got {
						if got[i] != want[i] {
							t.Errorf("expected bypass labels %v, got %v", want, got)
							break
						}
					}
				}
			}
			if tt.wantBypassA == nil && bypassAnnotations != nil {
				t.Errorf("expected nil bypass annotations, got %v", bypassAnnotations)
			}
			if tt.wantBypassA != nil {
				got := mapKeys(bypassAnnotations)
				sort.Strings(got)
				want := append([]string(nil), tt.wantBypassA...)
				sort.Strings(want)
				if len(got) != len(want) {
					t.Errorf("expected bypass annotations %v, got %v", want, got)
				} else {
					for i := range got {
						if got[i] != want[i] {
							t.Errorf("expected bypass annotations %v, got %v", want, got)
							break
						}
					}
				}
			}
			if allNamespaces != tt.wantAllNS {
				t.Errorf("expected allNamespaces=%v, got %v", tt.wantAllNS, allNamespaces)
			}
		})
	}
}

func TestBindingAdmissionHandler_Handle(t *testing.T) {
	tests := []struct {
		name       string
		featureOn  bool
		userNames  []string
		ns         string
		includeNS  map[string]struct{}
		excludeNS  map[string]struct{}
		allNS      bool
		dryRun     bool
		operation  admissionv1.Operation
		resource   string
		subRes     string
		username   string
		wantAllow  bool
		wantReason string
	}{
		// Escape tests
		{name: "feature off - allow", featureOn: false, userNames: []string{"koord"},
			operation: admissionv1.Create, resource: "pods", subRes: "binding",
			username: "kube-scheduler", wantAllow: true},
		{name: "empty whitelist - allow", featureOn: true, userNames: nil,
			operation: admissionv1.Create, resource: "pods", subRes: "binding",
			username: "kube-scheduler", wantAllow: true},

		// Whitelist match
		{name: "username match - allow", featureOn: true, userNames: []string{"koord-scheduler"},
			operation: admissionv1.Create, resource: "pods", subRes: "binding",
			username: "system:sa:koord-scheduler", wantAllow: true},

		// Multi-name
		{name: "multi user-name second match", featureOn: true, userNames: []string{"a", "koord"},
			operation: admissionv1.Create, resource: "pods", subRes: "binding",
			username: "system:koord", wantAllow: true},

		// Deny
		{name: "no match - deny", featureOn: true, userNames: []string{"koord-scheduler"},
			allNS: true, excludeNS: map[string]struct{}{},
			operation: admissionv1.Create, resource: "pods", subRes: "binding",
			username: "kube-scheduler", ns: "default", wantAllow: false,
			wantReason: "binding denied: username is not in the allowed list"},

		// Namespace exclusion
		{name: "excluded ns - allow", featureOn: true, userNames: []string{"koord-scheduler"},
			allNS: true, excludeNS: map[string]struct{}{"kube-system": {}},
			operation: admissionv1.Create, resource: "pods", subRes: "binding",
			username: "kube-scheduler", ns: "kube-system", wantAllow: true},

		// Namespace gray scope
		{name: "ns not in scope - allow", featureOn: true, userNames: []string{"koord-scheduler"},
			includeNS: map[string]struct{}{"test": {}}, excludeNS: map[string]struct{}{},
			operation: admissionv1.Create, resource: "pods", subRes: "binding",
			username: "kube-scheduler", ns: "production", wantAllow: true},
		{name: "ns in scope - deny", featureOn: true, userNames: []string{"koord-scheduler"},
			includeNS: map[string]struct{}{"test": {}}, excludeNS: map[string]struct{}{},
			operation: admissionv1.Create, resource: "pods", subRes: "binding",
			username: "kube-scheduler", ns: "test", wantAllow: false,
			wantReason: "binding denied: username is not in the allowed list"},

		// Dry-run
		{name: "dry-run - allow (would deny)", featureOn: true, userNames: []string{"koord-scheduler"},
			allNS: true, excludeNS: map[string]struct{}{}, dryRun: true,
			operation: admissionv1.Create, resource: "pods", subRes: "binding",
			username: "kube-scheduler", ns: "default", wantAllow: true},

		// Skip non-binding
		{name: "non-binding - skip", featureOn: true, userNames: []string{"koord"},
			operation: admissionv1.Create, resource: "pods", subRes: "",
			username: "kube-scheduler", wantAllow: true},
		{name: "non-create - skip", featureOn: true, userNames: []string{"koord"},
			operation: admissionv1.Update, resource: "pods", subRes: "binding",
			username: "kube-scheduler", wantAllow: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer utilfeature.SetFeatureGateDuringTest(t, utilfeature.DefaultMutableFeatureGate,
				features.BindingAdmissionWebhook, tt.featureOn)()

			s := save()
			defer restore(s)

			allowedUserNames = tt.userNames
			includedNS = tt.includeNS
			excludedNS = tt.excludeNS
			allNamespaces = tt.allNS
			BindingAdmissionDryRun = tt.dryRun

			handler := &BindingAdmissionHandler{Client: nil}
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation:   tt.operation,
					Resource:    metav1.GroupVersionResource{Group: "", Version: "v1", Resource: tt.resource},
					SubResource: tt.subRes,
					Name:        "test-pod",
					Namespace:   tt.ns,
					UserInfo:    authenticationv1.UserInfo{Username: tt.username},
				},
			}

			resp := handler.Handle(context.TODO(), req)
			if resp.Allowed != tt.wantAllow {
				t.Errorf("allowed = %v, want %v", resp.Allowed, tt.wantAllow)
			}
			if tt.wantReason != "" && (resp.Result == nil || resp.Result.Message != tt.wantReason) {
				got := ""
				if resp.Result != nil {
					got = resp.Result.Message
				}
				t.Errorf("reason = %q, want %q", got, tt.wantReason)
			}
		})
	}
}

func TestBindingAdmissionHandler_BypassLabelAndAnnotation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name      string
		pod       *corev1.Pod
		username  string
		bLabels   map[string]struct{}
		bAnnot    map[string]struct{}
		wantAllow bool
	}{
		// Default bypass label (backward compatible)
		{
			name: "default bypass label present - allow",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Labels: map[string]string{"koordinator.sh/binding-admission-bypass": "true"},
			}},
			username:  "kube-scheduler",
			bLabels:   map[string]struct{}{"koordinator.sh/binding-admission-bypass": {}},
			wantAllow: true,
		},
		{
			name: "default bypass label any value - allow (presence-based)",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Labels: map[string]string{"koordinator.sh/binding-admission-bypass": "anything"},
			}},
			username:  "kube-scheduler",
			bLabels:   map[string]struct{}{"koordinator.sh/binding-admission-bypass": {}},
			wantAllow: true,
		},
		{
			name: "no bypass label - deny",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default", Labels: map[string]string{"app": "x"},
			}},
			username:  "kube-scheduler",
			bLabels:   map[string]struct{}{"koordinator.sh/binding-admission-bypass": {}},
			wantAllow: false,
		},

		// Custom configurable bypass label
		{
			name: "custom bypass label - allow",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Labels: map[string]string{"app.kubernetes.io/managed-by": "virtual-kubelet"},
			}},
			username:  "kube-scheduler",
			bLabels:   map[string]struct{}{"app.kubernetes.io/managed-by": {}},
			wantAllow: true,
		},
		{
			name: "multiple bypass labels, one matches - allow",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Labels: map[string]string{"custom.io/delegated": "true"},
			}},
			username: "kube-scheduler",
			bLabels: map[string]struct{}{
				"koordinator.sh/binding-admission-bypass": {},
				"custom.io/delegated":                     {},
			},
			wantAllow: true,
		},
		{
			name: "multiple bypass labels, none matches - deny",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Labels: map[string]string{"app": "web"},
			}},
			username: "kube-scheduler",
			bLabels: map[string]struct{}{
				"koordinator.sh/binding-admission-bypass": {},
				"custom.io/delegated":                     {},
			},
			wantAllow: false,
		},

		// Bypass annotation
		{
			name: "bypass annotation present - allow",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Annotations: map[string]string{"custom.io/delegated-scheduler": "vk"},
			}},
			username:  "kube-scheduler",
			bLabels:   nil,
			bAnnot:    map[string]struct{}{"custom.io/delegated-scheduler": {}},
			wantAllow: true,
		},
		{
			name: "bypass annotation absent - deny",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Annotations: map[string]string{"other": "value"},
			}},
			username:  "kube-scheduler",
			bLabels:   nil,
			bAnnot:    map[string]struct{}{"custom.io/delegated-scheduler": {}},
			wantAllow: false,
		},

		// Both label and annotation configured
		{
			name: "label miss, annotation hit - allow",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Labels:      map[string]string{"app": "web"},
				Annotations: map[string]string{"custom.io/producer": "delegated"},
			}},
			username:  "kube-scheduler",
			bLabels:   map[string]struct{}{"custom.io/delegated": {}},
			bAnnot:    map[string]struct{}{"custom.io/producer": {}},
			wantAllow: true,
		},

		// Username match skips pod cache entirely
		{
			name: "username match - skip pod check",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default", Labels: map[string]string{},
			}},
			username:  "system:sa:koord-scheduler",
			bLabels:   map[string]struct{}{"koordinator.sh/binding-admission-bypass": {}},
			wantAllow: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer utilfeature.SetFeatureGateDuringTest(t, utilfeature.DefaultMutableFeatureGate,
				features.BindingAdmissionWebhook, true)()

			s := save()
			defer restore(s)

			allowedUserNames = []string{"koord-scheduler"}
			allNamespaces = true
			excludedNS = map[string]struct{}{}
			bypassLabels = tt.bLabels
			bypassAnnotations = tt.bAnnot
			BindingAdmissionDryRun = false

			fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.pod).Build()
			handler := &BindingAdmissionHandler{Client: fc}

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation:   admissionv1.Create,
					Resource:    metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
					SubResource: "binding",
					Name:        "p", Namespace: "default",
					UserInfo: authenticationv1.UserInfo{Username: tt.username},
				},
			}

			resp := handler.Handle(context.TODO(), req)
			if resp.Allowed != tt.wantAllow {
				t.Errorf("allowed = %v, want %v", resp.Allowed, tt.wantAllow)
			}
		})
	}
}

func TestInitBindingAdmissionFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	InitBindingAdmissionFlags(fs)

	// Verify all flags are registered with expected defaults.
	expected := map[string]string{
		"binding-admission-user-name":          "",
		"binding-admission-dry-run":            "false",
		"binding-admission-namespaces":         "",
		"binding-admission-exclude-namespaces": "kube-system",
		"binding-admission-bypass-labels":      "koordinator.sh/binding-admission-bypass",
		"binding-admission-bypass-annotations": "",
	}
	for name, want := range expected {
		f := fs.Lookup(name)
		if f == nil {
			t.Errorf("flag %q not registered", name)
			continue
		}
		if f.DefValue != want {
			t.Errorf("flag %q default = %q, want %q", name, f.DefValue, want)
		}
	}

	// Verify flags can be parsed.
	err := fs.Parse([]string{
		"--binding-admission-user-name=koord-scheduler,custom",
		"--binding-admission-bypass-labels=custom.io/skip",
		"--binding-admission-bypass-annotations=custom.io/ann",
	})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
}

func TestBindingAdmissionHandler_FailOpen(t *testing.T) {
	// When Client.Get fails (pod not found in cache), should fail-open (allow).
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	defer utilfeature.SetFeatureGateDuringTest(t, utilfeature.DefaultMutableFeatureGate,
		features.BindingAdmissionWebhook, true)()

	s := save()
	defer restore(s)

	allowedUserNames = []string{"koord-scheduler"}
	allNamespaces = true
	excludedNS = map[string]struct{}{}
	bypassLabels = map[string]struct{}{"koordinator.sh/binding-admission-bypass": {}}
	bypassAnnotations = nil
	BindingAdmissionDryRun = false

	// Build a client with NO objects so Get will return NotFound.
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	handler := &BindingAdmissionHandler{Client: fc}

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation:   admissionv1.Create,
			Resource:    metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			SubResource: "binding",
			Name:        "nonexistent-pod", Namespace: "default",
			UserInfo: authenticationv1.UserInfo{Username: "kube-scheduler"},
		},
	}

	resp := handler.Handle(context.TODO(), req)
	if !resp.Allowed {
		t.Errorf("expected fail-open (allow) when pod not in cache, got denied")
	}
}

func TestSetupBindingAdmission_Logging(t *testing.T) {
	// Covers logging branches in SetupBindingAdmission:
	// - includedNS non-empty, excludedNS non-empty, dryRun=true.
	s := save()
	defer restore(s)

	BindingAdmissionUserName = "koord"
	BindingAdmissionNamespaces = "ns-a,ns-b"
	BindingAdmissionExcludeNamespaces = "kube-system"
	BindingAdmissionBypassLabels = "bypass-key"
	BindingAdmissionBypassAnnotations = "bypass-ann"
	BindingAdmissionDryRun = true
	SetupBindingAdmission()

	if len(includedNS) != 2 {
		t.Errorf("expected 2 included namespaces, got %d", len(includedNS))
	}
	if len(excludedNS) != 1 {
		t.Errorf("expected 1 excluded namespace, got %d", len(excludedNS))
	}
	if !BindingAdmissionDryRun {
		t.Errorf("expected dry-run enabled")
	}
}

func TestBindingAdmissionHandler_NoIncludedNS(t *testing.T) {
	// When includedNS is empty and allNamespaces is false, no enforcement.
	defer utilfeature.SetFeatureGateDuringTest(t, utilfeature.DefaultMutableFeatureGate,
		features.BindingAdmissionWebhook, true)()

	s := save()
	defer restore(s)

	allowedUserNames = []string{"koord-scheduler"}
	allNamespaces = false
	includedNS = nil // empty = no enforcement
	excludedNS = map[string]struct{}{}
	BindingAdmissionDryRun = false

	handler := &BindingAdmissionHandler{Client: nil}
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation:   admissionv1.Create,
			Resource:    metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			SubResource: "binding",
			Name:        "test-pod", Namespace: "default",
			UserInfo: authenticationv1.UserInfo{Username: "kube-scheduler"},
		},
	}

	resp := handler.Handle(context.TODO(), req)
	if !resp.Allowed {
		t.Errorf("expected allow when includedNS is empty (no enforcement), got denied")
	}
}
