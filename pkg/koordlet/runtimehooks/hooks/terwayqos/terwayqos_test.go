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

package terwayqos

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func TestParseQuantityWithNilValue(t *testing.T) {
	result, err := parseQuantity(nil, 100)
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), result)
}

func TestParseQuantityWithStringTypeAndValidValue(t *testing.T) {
	val := intstr.FromString("40")
	result, err := parseQuantity(&val, 100)
	assert.NoError(t, err)
	assert.Equal(t, uint64(5), result)
}

func TestParseQuantityWithStringTypeAndValueExceedingTotal(t *testing.T) {
	val := intstr.FromString("808")
	result, err := parseQuantity(&val, 100)
	assert.Error(t, err)
	assert.Equal(t, uint64(0), result)
}

func TestParseQuantityWithIntType(t *testing.T) {
	val := intstr.FromInt(50)
	result, err := parseQuantity(&val, 100)
	assert.NoError(t, err)
	assert.Equal(t, uint64(50), result)
}

func TestGetPodQoSWithValidAnnotation(t *testing.T) {
	anno := map[string]string{
		extension.AnnotationNetworkQOS: `{"IngressLimit": "1000", "EgressLimit": "2000"}`,
	}

	ingress, egress, err := getPodQoS(anno)

	assert.NoError(t, err)
	assert.Equal(t, uint64(125), ingress)
	assert.Equal(t, uint64(250), egress)
}

func TestGetPodQoSWithInvalidAnnotation(t *testing.T) {
	anno := map[string]string{
		extension.AnnotationNetworkQOS: `{"IngressLimit": "invalid", "EgressLimit": "2000"}`,
	}

	_, _, err := getPodQoS(anno)

	assert.Error(t, err)
}

func TestGetPodQoSWithNoAnnotation(t *testing.T) {
	anno := map[string]string{}

	ingress, egress, err := getPodQoS(anno)

	assert.NoError(t, err)
	assert.Equal(t, uint64(0), ingress)
	assert.Equal(t, uint64(0), egress)
}

func TestPodPriorityWithExistingLabel(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				extension.LabelPodQoS: "BE",
			},
		},
	}

	prio := getPodPrio(pod)

	assert.Equal(t, 2, prio)
}

func TestPodPriorityWithQOSGuaranteed(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			QOSClass: corev1.PodQOSGuaranteed,
		},
	}

	prio := getPodPrio(pod)

	assert.Equal(t, 1, prio)
}

func TestPodPriorityWithQOSBurstable(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			QOSClass: corev1.PodQOSBurstable,
		},
	}

	prio := getPodPrio(pod)

	assert.Equal(t, 1, prio)
}

func TestPodPriorityWithQOSBestEffort(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			QOSClass: corev1.PodQOSBestEffort,
		},
	}

	prio := getPodPrio(pod)

	assert.Equal(t, 2, prio)
}

func TestPodPriorityWithNoQOSClass(t *testing.T) {
	pod := &corev1.Pod{}

	prio := getPodPrio(pod)

	assert.Equal(t, 0, prio)
}

func TestParseQoSWithNone(t *testing.T) {
	qosConf := &slov1alpha1.NetworkQOSCfg{}
	qos, err := parseQoS(qosConf, 0)
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), qos.IngressLimitBps)
}

func TestParseQoSWithDisabled(t *testing.T) {
	enable := false
	qosConf := &slov1alpha1.NetworkQOSCfg{Enable: &enable}
	qos, err := parseQoS(qosConf, 0)
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), qos.IngressLimitBps)
}

func TestParseQoSWithEnabled(t *testing.T) {
	enable := true
	v := intstr.FromString("10M")
	qosConf := &slov1alpha1.NetworkQOSCfg{
		Enable: &enable,
		NetworkQOS: slov1alpha1.NetworkQOS{
			IngressRequest: &v,
			IngressLimit:   &v,
			EgressRequest:  &v,
			EgressLimit:    &v,
		},
	}
	qos, err := parseQoS(qosConf, 100*1000*1000)
	assert.NoError(t, err)
	assert.Equal(t, uint64(10*1000*1000/8), qos.IngressLimitBps)
}

func TestParseRuleForNodeSLOWithValidQoSPolicy(t *testing.T) {
	plugin := newPlugin()

	terway := slov1alpha1.NETQOSPolicyTerwayQos
	mergedNodeSLO := &slov1alpha1.NodeSLOSpec{
		ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
			Policies: &slov1alpha1.ResourceQOSPolicies{
				NETQOSPolicy: &terway,
			},
		},
	}

	result, err := plugin.parseRuleForNodeSLO(mergedNodeSLO)

	assert.NoError(t, err)
	assert.True(t, result)
	assert.NotNil(t, plugin.node)
	assert.True(t, *plugin.enabled)
}

func TestParseRuleForNodeSLOWithNoQoSPolicy(t *testing.T) {
	plugin := newPlugin()
	mergedNodeSLO := &slov1alpha1.NodeSLOSpec{}

	result, err := plugin.parseRuleForNodeSLO(mergedNodeSLO)

	assert.NoError(t, err)
	assert.True(t, result)
	assert.False(t, *plugin.enabled)
}

func TestParseNetQoS(t *testing.T) {
	enable := true
	v1 := intstr.FromString("10M")
	v2 := intstr.FromString("20M")

	slo := &slov1alpha1.NodeSLOSpec{
		ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
			LSClass: &slov1alpha1.ResourceQOS{
				NetworkQOS: &slov1alpha1.NetworkQOSCfg{
					Enable: &enable,
					NetworkQOS: slov1alpha1.NetworkQOS{
						IngressRequest: &v1,
						IngressLimit:   &v1,
						EgressRequest:  &v1,
						EgressLimit:    &v1,
					},
				},
			},
			BEClass: &slov1alpha1.ResourceQOS{
				NetworkQOS: &slov1alpha1.NetworkQOSCfg{
					Enable: &enable,
					NetworkQOS: slov1alpha1.NetworkQOS{
						IngressRequest: &v2,
						IngressLimit:   &v2,
						EgressRequest:  &v2,
						EgressLimit:    &v2,
					},
				},
			},
		},
		SystemStrategy: &slov1alpha1.SystemStrategy{
			TotalNetworkBandwidth: resource.MustParse("100M"),
		},
	}
	node := &Node{}

	err := parseNetQoS(slo, node)

	assert.NoError(t, err)
	assert.Equal(t, uint64(100*1000*1000/8), node.HwRxBpsMax)
	assert.Equal(t, uint64(10*1000*1000/8), node.L1TxBpsMin)
	assert.Equal(t, uint64(20*1000*1000/8), node.L2TxBpsMin)
	assert.Equal(t, uint64(10*1000*1000/8), node.L1TxBpsMax)
	assert.Equal(t, uint64(20*1000*1000/8), node.L2TxBpsMax)
}
