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

// Package frameworkext_test holds tests that need to import scheduler plugins.
// The plugins import frameworkext, so an in-package test would be a cycle.
package frameworkext_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fwktype "k8s.io/kube-scheduler/framework"
	k8sframework "k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"
)

// signingPlugin signs the pod's value of a label, so two pods agree only when
// that label agrees.
type signingPlugin struct {
	name  string
	label string
	// refuseLabel opts a pod out of batching when it carries this label, the
	// shape Coscheduling uses for network-topology-aware pods.
	refuseLabel string
}

func (p *signingPlugin) Name() string { return p.name }

func (p *signingPlugin) PreFilter(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	return nil, nil
}
func (p *signingPlugin) PreFilterExtensions() fwktype.PreFilterExtensions { return nil }

func (p *signingPlugin) SignPod(_ context.Context, pod *corev1.Pod) ([]fwktype.SignFragment, *fwktype.Status) {
	if p.refuseLabel != "" && pod.Labels[p.refuseLabel] != "" {
		return nil, fwktype.NewStatus(fwktype.Unschedulable, "not eligible for batching")
	}
	return []fwktype.SignFragment{{Key: p.name + "." + p.label, Value: pod.Labels[p.label]}}, nil
}

// silentPlugin participates in an extension point without signing, which is
// what disables signatures for the whole profile.
type silentPlugin struct{ name string }

func (p *silentPlugin) Name() string { return p.name }
func (p *silentPlugin) PreFilter(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	return nil, nil
}
func (p *silentPlugin) PreFilterExtensions() fwktype.PreFilterExtensions { return nil }
func (p *silentPlugin) PreScore(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) *fwktype.Status {
	return nil
}
func (p *silentPlugin) Score(context.Context, fwktype.CycleState, *corev1.Pod, fwktype.NodeInfo) (int64, *fwktype.Status) {
	return 0, nil
}
func (p *silentPlugin) ScoreExtensions() fwktype.ScoreExtensions { return nil }

func newPod(name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: "default", UID: types.UID(name), Labels: labels,
	}}
}

func newFrameworkWith(t *testing.T, plugins ...schedulertesting.RegisterPluginFunc) k8sframework.Framework {
	t.Helper()
	registered := append([]schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}, plugins...)
	fh, err := schedulertesting.NewFramework(context.TODO(), registered, "koord-scheduler",
		frameworkruntime.WithSnapshotSharedLister(nil))
	require.NoError(t, err)
	return fh
}

func signer(name, label, refuseLabel string, extensions ...string) schedulertesting.RegisterPluginFunc {
	return schedulertesting.RegisterPluginAsExtensions(name,
		func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &signingPlugin{name: name, label: label, refuseLabel: refuseLabel}, nil
		}, extensions...)
}

func silent(name string, extensions ...string) schedulertesting.RegisterPluginFunc {
	return schedulertesting.RegisterPluginAsExtensions(name,
		func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
			return &silentPlugin{name: name}, nil
		}, extensions...)
}

// TestProfileSignatureCoverage pins the profile-level contract the per-plugin
// SignPod tests cannot reach. computeBatchablePlugins runs at framework
// construction over the PreFilter, Filter, PreScore and Score plugins, and a
// single one without SignPlugin turns signatures off for every pod in the
// profile. Asserting only that two signatures are equal would pass in exactly
// that case, because both are nil, so each case checks for a non-empty
// signature first.
func TestProfileSignatureCoverage(t *testing.T) {
	same := newPod("a", map[string]string{"tier": "gold"})
	other := newPod("b", map[string]string{"tier": "gold"})
	different := newPod("c", map[string]string{"tier": "silver"})
	refused := newPod("d", map[string]string{"tier": "gold", "topology": "yes"})

	t.Run("a fully signing profile produces comparable signatures", func(t *testing.T) {
		fh := newFrameworkWith(t, signer("sign-prefilter", "tier", "", "PreFilter"))
		sigA := fh.SignPod(context.TODO(), same, false)
		sigB := fh.SignPod(context.TODO(), other, false)
		sigC := fh.SignPod(context.TODO(), different, false)
		require.NotEmpty(t, sigA)
		require.NotEmpty(t, sigB)
		assert.Equal(t, sigA, sigB, "pods agreeing on every signed input share a signature")
		assert.NotEqual(t, sigA, sigC, "a differing signed input must separate them")
	})

	t.Run("a refusing signer opts out only that pod", func(t *testing.T) {
		fh := newFrameworkWith(t, signer("sign-prefilter", "tier", "topology", "PreFilter"))
		require.NotEmpty(t, fh.SignPod(context.TODO(), same, false))
		assert.Empty(t, fh.SignPod(context.TODO(), refused, false),
			"an Unschedulable status from one signer drops the whole signature for that pod")
	})

	// The extension points are listed separately because computeBatchablePlugins
	// collects all four, not just Filter and Score.
	for _, extension := range []string{"PreFilter", "PreScore", "Score"} {
		t.Run("an unsigned "+extension+" plugin disables the profile", func(t *testing.T) {
			fh := newFrameworkWith(t,
				signer("sign-prefilter", "tier", "", "PreFilter"),
				silent("silent-plugin", extension),
			)
			assert.Empty(t, fh.SignPod(context.TODO(), same, false),
				"one plugin without SignPlugin turns signatures off for the profile")
			assert.Empty(t, fh.SignPod(context.TODO(), different, false))
		})
	}
}
