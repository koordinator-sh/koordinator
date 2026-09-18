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

package frameworkext_test

import (
	"context"
	"testing"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota"

	nrtclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned"

	nrtfake "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"

	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/nodenumaresource"

	fwktype "k8s.io/kube-scheduler/framework"

	pgclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"
	pgfake "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned/fake"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling"

	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/kubernetes/pkg/features"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/schedulinghint"

	apiruntime "k8s.io/apimachinery/pkg/runtime"
	schedulerapi "k8s.io/kubernetes/pkg/scheduler/apis/config"

	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	v1schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8sframework "k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/deviceshare"
)

// registerWithArgs registers a plugin at the given extension points and gives
// it a plugin config. The upstream registrar tries to decode <Name>Args from
// the kube-scheduler config scheme, which leaves a nil object for a
// Koordinator plugin, and the production factory rejects that. Any entry it
// produced is replaced rather than added to, since the framework refuses a
// repeated config.
func registerWithArgs(name string, factory frameworkruntime.PluginFactory, args apiruntime.Object, extensions ...string) schedulertesting.RegisterPluginFunc {
	inner := schedulertesting.RegisterPluginAsExtensions(name, factory, extensions...)
	return func(reg *frameworkruntime.Registry, profile *schedulerapi.KubeSchedulerProfile) {
		inner(reg, profile)
		kept := profile.PluginConfig[:0]
		for _, pc := range profile.PluginConfig {
			if pc.Name != name {
				kept = append(kept, pc)
			}
		}
		profile.PluginConfig = append(kept, schedulerapi.PluginConfig{Name: name, Args: args})
	}
}

func deviceShareArgs() *schedulerconfig.DeviceShareArgs {
	v1Args := &v1schedulerconfig.DeviceShareArgs{}
	v1schedulerconfig.SetDefaults_DeviceShareArgs(v1Args)
	args := &schedulerconfig.DeviceShareArgs{}
	_ = v1schedulerconfig.Convert_v1_DeviceShareArgs_To_config_DeviceShareArgs(v1Args, args, nil)
	return args
}

// newRealPluginFramework assembles a framework the way the scheduler does, with
// the production factory registered before construction. computeBatchablePlugins
// runs during construction, so a factory called afterwards would not be part of
// the profile's signature at all.
func newRealPluginFramework(t *testing.T, plugins ...schedulertesting.RegisterPluginFunc) k8sframework.Framework {
	t.Helper()
	// computeBatchablePlugins runs during construction and only when the gate
	// is on, so it is set here rather than left to whatever another test left
	// behind.
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.OpportunisticBatching, true)
	frameworkexthelper.ResetRegistrations()
	registered := append([]schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}, plugins...)

	cs := kubefake.NewSimpleClientset()
	fh, err := schedulertesting.NewFramework(context.TODO(), registered, "koord-scheduler",
		frameworkruntime.WithClientSet(cs),
		frameworkruntime.WithInformerFactory(informers.NewSharedInformerFactory(cs, 0)),
		frameworkruntime.WithSnapshotSharedLister(nil))
	require.NoError(t, err)
	return fh
}

func deviceShareFactory(t *testing.T) frameworkruntime.PluginFactory {
	t.Helper()
	koordClientSet := koordfake.NewSimpleClientset()
	extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordinformers.NewSharedInformerFactory(koordClientSet, 0)),
		frameworkext.WithReservationNominator(frameworkext.NewFakeReservationNominator()),
		frameworkext.WithReservationCache(frameworkext.NewFakeReservationCache()),
	)
	require.NoError(t, err)
	return frameworkext.PluginFactoryProxy(extenderFactory, deviceshare.New)
}

// TestProfileSignature_DeviceShareIsPartOfTheSignature drives the real
// DeviceShare plugin through the framework's own SignPod.
//
// DeviceShare is the only signer configured here, so any difference between
// these signatures is its doing. That matters: frameworkImpl.SignPod always
// seeds the map with the scheduler name, so a non-empty signature on its own
// says nothing about whether a plugin contributed. Keeping the Reservation
// plugin out is deliberate too, since it signs pod UID and name and would make
// every pair differ regardless.
func TestProfileSignature_DeviceShareIsPartOfTheSignature(t *testing.T) {
	fh := newRealPluginFramework(t, registerWithArgs(
		deviceshare.Name, deviceShareFactory(t), deviceShareArgs(), "PreFilter", "Filter"))

	// Half a GPU, which is what makes calcDesiredRequestsAndCountForGPU report
	// a shared request and what makes Filter consult the provider label.
	sharedGPU := func(name string, labels map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", UID: types.UID(name), Labels: labels,
			},
			Spec: corev1.PodSpec{
				SchedulerName: "koord-scheduler",
				Containers: []corev1.Container{{
					Name: "c",
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						apiext.ResourceGPU: resource.MustParse("50"),
					}},
				}},
			},
		}
	}
	hami := map[string]string{apiext.LabelGPUIsolationProvider: string(apiext.GPUIsolationProviderHAMICore)}
	other := map[string]string{apiext.LabelGPUIsolationProvider: "other-provider"}

	ctx := context.TODO()
	none := fh.SignPod(ctx, sharedGPU("a", nil), false)
	withHAMI := fh.SignPod(ctx, sharedGPU("b", hami), false)
	withOther := fh.SignPod(ctx, sharedGPU("c", other), false)
	sameAsHAMI := fh.SignPod(ctx, sharedGPU("d", hami), false)

	require.NotEmpty(t, none)
	require.NotEmpty(t, withHAMI)

	assert.Equal(t, withHAMI, sameAsHAMI,
		"two pods differing only in name and UID must share a signature in this profile")
	assert.NotEqual(t, none, withHAMI,
		"the GPUIsolationProvider label must reach the profile signature")
	assert.NotEqual(t, withHAMI, withOther,
		"a different provider must reach the profile signature")
}

// koordinatorFactory wraps a Koordinator plugin factory the way the scheduler
// does, so the plugin the framework builds is the production one.
func koordinatorFactory(t *testing.T, factory frameworkruntime.PluginFactory) frameworkruntime.PluginFactory {
	t.Helper()
	koordClientSet := koordfake.NewSimpleClientset()
	extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordinformers.NewSharedInformerFactory(koordClientSet, 0)),
		frameworkext.WithReservationNominator(frameworkext.NewFakeReservationNominator()),
		frameworkext.WithReservationCache(frameworkext.NewFakeReservationCache()),
	)
	require.NoError(t, err)
	return frameworkext.PluginFactoryProxy(extenderFactory, factory)
}

// TestProfileSignature_UnsupportedPluginDisablesTheProfile uses a real
// Koordinator plugin that has no SignPlugin. This is the boundary the PR scope
// section describes, and it is what makes comparing two signatures for equality
// unsafe on its own: with the profile disabled both are nil.
func TestProfileSignature_UnsupportedPluginDisablesTheProfile(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "p", Namespace: "default", UID: "p",
	}, Spec: corev1.PodSpec{SchedulerName: "koord-scheduler"}}

	signed := newRealPluginFramework(t, registerWithArgs(
		deviceshare.Name, koordinatorFactory(t, deviceshare.New), deviceShareArgs(), "PreFilter", "Filter"))
	require.NotEmpty(t, signed.SignPod(context.TODO(), pod, false))

	withUnsupported := newRealPluginFramework(t,
		registerWithArgs(deviceshare.Name, koordinatorFactory(t, deviceshare.New), deviceShareArgs(), "PreFilter", "Filter"),
		schedulertesting.RegisterPluginAsExtensions(schedulinghint.Name, koordinatorFactory(t, schedulinghint.New), "PreFilter"),
	)
	assert.Empty(t, withUnsupported.SignPod(context.TODO(), pod, false),
		"SchedulingHint has no SignPlugin, so configuring it turns signatures off for the whole profile")
}

// TestProfileSignature_ReservationSignsPodIdentity records a deliberate
// trade-off rather than a defect. The Reservation signer includes pod UID and
// name because owner matching reads them, so ordinary pods do not share a
// signature in a profile that enables it. Any test that wants to prove another
// plugin's fragment reached the signature has to leave Reservation out.
func TestProfileSignature_ReservationSignsPodIdentity(t *testing.T) {
	fh := newRealPluginFramework(t, registerWithArgs(
		reservation.Name, koordinatorFactory(t, reservation.New), reservationArgs(), "PreFilter", "Filter"))

	mkPod := func(name string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "default", UID: types.UID(name),
		}, Spec: corev1.PodSpec{SchedulerName: "koord-scheduler"}}
	}
	ctx := context.TODO()
	a := fh.SignPod(ctx, mkPod("a"), false)
	b := fh.SignPod(ctx, mkPod("b"), false)
	require.NotEmpty(t, a)
	require.NotEmpty(t, b)
	assert.NotEqual(t, a, b, "the owner-matching inputs include pod UID and name")
}

func reservationArgs() *schedulerconfig.ReservationArgs {
	v1Args := &v1schedulerconfig.ReservationArgs{}
	v1schedulerconfig.SetDefaults_ReservationArgs(v1Args)
	args := &schedulerconfig.ReservationArgs{}
	_ = v1schedulerconfig.Convert_v1_ReservationArgs_To_config_ReservationArgs(v1Args, args, nil)
	return args
}

// schedPluginsHandle satisfies the scheduler-plugins clientset that
// coscheduling.New and elasticquota.New look for on the handle, so neither
// falls back to building one from a kubeconfig the test framework has none of.
type schedPluginsHandle struct {
	frameworkext.ExtendedHandle
	pgclientset.Interface
}

func coschedulingArgs() *schedulerconfig.CoschedulingArgs {
	v1Args := &v1schedulerconfig.CoschedulingArgs{}
	v1schedulerconfig.SetDefaults_CoschedulingArgs(v1Args)
	args := &schedulerconfig.CoschedulingArgs{}
	_ = v1schedulerconfig.Convert_v1_CoschedulingArgs_To_config_CoschedulingArgs(v1Args, args, nil)
	return args
}

// schedPluginsFactory wraps a factory whose plugin needs that clientset.
func schedPluginsFactory(t *testing.T, factory frameworkruntime.PluginFactory) frameworkruntime.PluginFactory {
	t.Helper()
	pgClient := pgfake.NewSimpleClientset()
	// The wrapping happens inside the proxy, which needs the real framework
	// itself, and it embeds ExtendedHandle so the Koordinator methods
	// coscheduling.New reaches for are still there.
	return koordinatorFactory(t, func(ctx context.Context, args apiruntime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
		return factory(ctx, args, &schedPluginsHandle{
			ExtendedHandle: handle.(frameworkext.ExtendedHandle),
			Interface:      pgClient,
		})
	})
}

func coschedulingFactory(t *testing.T) frameworkruntime.PluginFactory {
	return schedPluginsFactory(t, coscheduling.New)
}

func elasticQuotaFactory(t *testing.T) frameworkruntime.PluginFactory {
	return schedPluginsFactory(t, elasticquota.New)
}

// TestProfileSignature_CoschedulingOptsOutTopologyPods drives the real
// Coscheduling plugin. A pod carrying the network-topology selector must lose
// its signature, because its ranking depends on where the rest of the gang
// went, while an ordinary pod in the same profile keeps one.
func TestProfileSignature_CoschedulingOptsOutTopologyPods(t *testing.T) {
	fh := newRealPluginFramework(t, registerWithArgs(
		coscheduling.Name, coschedulingFactory(t), coschedulingArgs(), "PreFilter", "PreScore", "Score"))

	mkPod := func(name string, annotations map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", UID: types.UID(name), Annotations: annotations,
			},
			Spec: corev1.PodSpec{SchedulerName: "koord-scheduler"},
		}
	}
	ctx := context.TODO()
	ordinary := fh.SignPod(ctx, mkPod("plain", nil), false)
	topologyAware := fh.SignPod(ctx, mkPod("topo", map[string]string{
		apiext.AnnotationPodNetworkTopologySelector: "topology-a",
	}), false)

	require.NotEmpty(t, ordinary, "an ordinary pod keeps a signature in this profile")
	assert.Empty(t, topologyAware,
		"a network-topology-aware pod must be refused by Coscheduling's signer")
}

// nodeNUMAHandle satisfies the NodeResourceTopology clientset that
// initNRTInformerFactory looks for, the same shape coscheduling needs for its
// PodGroup client.
type nodeNUMAHandle struct {
	frameworkext.ExtendedHandle
	nrtclientset.Interface
}

func nodeNUMAFactory(t *testing.T) frameworkruntime.PluginFactory {
	t.Helper()
	nrtClient := nrtfake.NewSimpleClientset()
	return koordinatorFactory(t, func(ctx context.Context, args apiruntime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
		return nodenumaresource.New(ctx, args, &nodeNUMAHandle{
			ExtendedHandle: handle.(frameworkext.ExtendedHandle),
			Interface:      nrtClient,
		})
	})
}

func nodeNUMAArgs() *schedulerconfig.NodeNUMAResourceArgs {
	v1Args := &v1schedulerconfig.NodeNUMAResourceArgs{}
	v1schedulerconfig.SetDefaults_NodeNUMAResourceArgs(v1Args)
	args := &schedulerconfig.NodeNUMAResourceArgs{}
	_ = v1schedulerconfig.Convert_v1_NodeNUMAResourceArgs_To_config_NodeNUMAResourceArgs(v1Args, args, nil)
	return args
}

// TestProfileSignature_NodeNUMACPUSetEligibility drives the real
// NodeNUMAResource plugin. The two pods carry the same labels and differ only
// in spec.Priority, which is what makes AllowUseCPUSet differ, and this plugin
// is the only signer here, so a difference in the signature is its doing.
func TestProfileSignature_NodeNUMACPUSetEligibility(t *testing.T) {
	fh := newRealPluginFramework(t, registerWithArgs(
		nodenumaresource.Name, nodeNUMAFactory(t), nodeNUMAArgs(),
		"PreFilter", "Filter", "Score"))

	mkPod := func(name string, priority *int32) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", UID: types.UID(name),
				Labels: map[string]string{apiext.LabelPodQoS: string(apiext.QoSLSE)},
			},
			Spec: corev1.PodSpec{
				SchedulerName: "koord-scheduler",
				Priority:      priority,
				Containers: []corev1.Container{{
					Name: "c",
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("2"),
					}},
				}},
			},
		}
	}
	ctx := context.TODO()
	// LSE with no explicit priority defaults to koord-prod, so AllowUseCPUSet
	// holds; a batch priority value takes it away.
	prod := fh.SignPod(ctx, mkPod("prod", nil), false)
	batch := fh.SignPod(ctx, mkPod("batch", ptr.To(apiext.PriorityBatchValueDefault)), false)

	require.NotEmpty(t, prod)
	require.NotEmpty(t, batch)
	assert.NotEqual(t, prod, batch,
		"CPU-set eligibility must reach the profile signature even though the labels match")
}

func elasticQuotaArgs() *schedulerconfig.ElasticQuotaArgs {
	v1Args := &v1schedulerconfig.ElasticQuotaArgs{}
	v1schedulerconfig.SetDefaults_ElasticQuotaArgs(v1Args)
	args := &schedulerconfig.ElasticQuotaArgs{}
	_ = v1schedulerconfig.Convert_v1_ElasticQuotaArgs_To_config_ElasticQuotaArgs(v1Args, args, nil)
	return args
}

// TestProfileSignature_ShippedProfileDisablesSignatures mirrors the preFilter
// list in config/manager/scheduler-config.yaml, where SchedulingHint is the
// first entry. It has no SignPlugin, so computeBatchablePlugins turns
// signatures off and every SignPod in the profile is inert. Dropping it from
// the same profile brings them back.
//
// SchedulingHint cannot be signed rather than merely has not been: its
// PreFilter narrows nodes from hinter.GetSchedulingHintState, a CycleState
// value written by pkg/scheduler/batch, and SignPod only sees the pod. The
// framework refusing to batch here is correct, so this test records the state
// of the shipped configuration and not a defect to be patched away.
func TestProfileSignature_ShippedProfileDisablesSignatures(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default", UID: "p"},
		Spec:       corev1.PodSpec{SchedulerName: "koord-scheduler"},
	}
	signers := func() []schedulertesting.RegisterPluginFunc {
		return []schedulertesting.RegisterPluginFunc{
			registerWithArgs(reservation.Name, koordinatorFactory(t, reservation.New), reservationArgs(), "PreFilter"),
			registerWithArgs(nodenumaresource.Name, nodeNUMAFactory(t), nodeNUMAArgs(), "PreFilter"),
			registerWithArgs(deviceshare.Name, koordinatorFactory(t, deviceshare.New), deviceShareArgs(), "PreFilter"),
			registerWithArgs(coscheduling.Name, coschedulingFactory(t), coschedulingArgs(), "PreFilter"),
			registerWithArgs(elasticquota.Name, elasticQuotaFactory(t), elasticQuotaArgs(), "PreFilter"),
		}
	}

	withoutHint := newRealPluginFramework(t, signers()...)
	require.NotEmpty(t, withoutHint.SignPod(context.TODO(), pod, false),
		"the five signing plugins from the shipped list do produce a signature on their own")

	shipped := append([]schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterPluginAsExtensions(
			schedulinghint.Name, koordinatorFactory(t, schedulinghint.New), "PreFilter"),
	}, signers()...)
	assert.Empty(t, newRealPluginFramework(t, shipped...).SignPod(context.TODO(), pod, false),
		"SchedulingHint leads the shipped preFilter list, so signatures are off in the default deployment")
}
