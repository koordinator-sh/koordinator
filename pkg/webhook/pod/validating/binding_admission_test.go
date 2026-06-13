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
	"reflect"
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
	userName     string
	dryRun       bool
	namespaces   string
	excludeNS    string
	allowedNames []string
	included     map[string]struct{}
	excluded     map[string]struct{}
	allNS        bool
}

func save() admissionState {
	return admissionState{
		userName: BindingAdmissionUserName, dryRun: BindingAdmissionDryRun,
		namespaces: BindingAdmissionNamespaces, excludeNS: BindingAdmissionExcludeNamespaces,
		allowedNames: allowedUserNames, included: includedNS, excluded: excludedNS, allNS: allNamespaces,
	}
}

func restore(s admissionState) {
	BindingAdmissionUserName = s.userName
	BindingAdmissionDryRun = s.dryRun
	BindingAdmissionNamespaces = s.namespaces
	BindingAdmissionExcludeNamespaces = s.excludeNS
	allowedUserNames = s.allowedNames
	includedNS = s.included
	excludedNS = s.excluded
	allNamespaces = s.allNS
}

func TestSetupBindingAdmission(t *testing.T) {
	tests := []struct {
		name      string
		userName  string
		ns        string
		excludeNS string
		dryRun    bool
		wantNames []string
		wantAllNS bool
	}{
		{name: "empty all", userName: "", ns: "", excludeNS: "", wantNames: nil, wantAllNS: false},
		{name: "single name", userName: "koord-scheduler", ns: "", excludeNS: "", wantNames: []string{"koord-scheduler"}},
		{name: "multi names", userName: "a,b,c", ns: "", excludeNS: "", wantNames: []string{"a", "b", "c"}},
		{name: "name trim spaces", userName: " a , b ", ns: "", excludeNS: "", wantNames: []string{"a", "b"}},
		{name: "star = all namespaces", userName: "x", ns: "*", excludeNS: "", wantNames: []string{"x"}, wantAllNS: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := save()
			defer restore(s)

			BindingAdmissionUserName = tt.userName
			BindingAdmissionNamespaces = tt.ns
			BindingAdmissionExcludeNamespaces = tt.excludeNS
			BindingAdmissionDryRun = tt.dryRun
			SetupBindingAdmission()

			if tt.wantNames == nil && allowedUserNames != nil {
				t.Errorf("expected nil user names, got %v", allowedUserNames)
			}
			if tt.wantNames != nil && !reflect.DeepEqual(allowedUserNames, tt.wantNames) {
				t.Errorf("expected user names %v, got %v", tt.wantNames, allowedUserNames)
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

func TestBindingAdmissionHandler_BypassLabel(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name      string
		pod       *corev1.Pod
		username  string
		wantAllow bool
	}{
		{
			name: "bypass label true - allow",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Labels: map[string]string{"koordinator.sh/binding-admission-bypass": "true"},
			}},
			username: "kube-scheduler", wantAllow: true,
		},
		{
			name: "no bypass label - deny",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default", Labels: map[string]string{"app": "x"},
			}},
			username: "kube-scheduler", wantAllow: false,
		},
		{
			name: "bypass label wrong value - deny",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default",
				Labels: map[string]string{"koordinator.sh/binding-admission-bypass": "false"},
			}},
			username: "kube-scheduler", wantAllow: false,
		},
		{
			name: "username match - skip label check",
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "p", Namespace: "default", Labels: map[string]string{},
			}},
			username: "system:sa:koord-scheduler", wantAllow: true,
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
