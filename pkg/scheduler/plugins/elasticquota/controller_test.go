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

package elasticquota

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	testing2 "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

func TestController_Run(t *testing.T) {
	ctx := context.TODO()
	cases := []struct {
		name          string
		elasticQuotas []*v1alpha1.ElasticQuota
		pods          []*v1.Pod
		want          []*v1alpha1.ElasticQuota
	}{
		{
			name: "no init Containers pod",
			elasticQuotas: []*v1alpha1.ElasticQuota{
				MakeEQ("t1-ns1", "t1-ns1").Min(MakeResourceList().CPU(3).Mem(5).Obj()).
					Max(MakeResourceList().CPU(5).Mem(15).GPU(1).Obj()).Obj(),
			},
			pods: []*v1.Pod{
				MakePod("t1-ns1", "pod1").Phase(v1.PodRunning).Container(
					MakeResourceList().CPU(1).Mem(2).GPU(1).Obj()).UID("pod1").Obj(),
				MakePod("t1-ns1", "pod2").Phase(v1.PodPending).Container(
					MakeResourceList().CPU(1).Mem(2).GPU(0).Obj()).UID("pod2").Obj(),
			},
			want: []*v1alpha1.ElasticQuota{
				MakeEQ("t1-ns1", "t1-ns1").
					Used(MakeResourceList().CPU(2).Mem(4).GPU(1).Obj()).Obj(),
			},
		},
		{
			name: "have init Containers pod",
			elasticQuotas: []*v1alpha1.ElasticQuota{
				MakeEQ("t2-ns1", "t2-ns1").
					Min(MakeResourceList().CPU(3).Mem(5).Obj()).
					Max(MakeResourceList().CPU(5).Mem(15).Obj()).Obj(),
			},
			pods: []*v1.Pod{
				// CPU: 2 Mem: 4
				MakePod("t2-ns1", "pod1").Phase(v1.PodRunning).
					Container(
						MakeResourceList().CPU(1).Mem(2).Obj()).
					Container(MakeResourceList().CPU(1).Mem(2).Obj()).UID("pod1").Obj(),
				//CPU: 3 Mem: 3
				MakePod("t2-ns1", "pod2").Phase(v1.PodRunning).
					InitContainerRequest(
						MakeResourceList().CPU(2).Mem(1).Obj()).
					InitContainerRequest(
						MakeResourceList().CPU(2).Mem(3).Obj()).
					Container(
						MakeResourceList().CPU(2).Mem(1).Obj()).
					Container(
						MakeResourceList().CPU(1).Mem(1).Obj()).UID("pod2").Obj(),
			},
			want: []*v1alpha1.ElasticQuota{
				MakeEQ("t2-ns1", "t2-ns1").
					Used(MakeResourceList().CPU(5).Mem(7).Obj()).Obj(),
			},
		},
		{
			name: "pods belongs to different quota",
			elasticQuotas: []*v1alpha1.ElasticQuota{
				MakeEQ("t3-ns1", "t3-ns1").
					Min(MakeResourceList().CPU(3).Mem(5).Obj()).
					Max(MakeResourceList().CPU(5).Mem(15).Obj()).Obj(),
				MakeEQ("t3-ns2", "t3-ns2").
					Min(MakeResourceList().CPU(3).Mem(5).Obj()).
					Max(MakeResourceList().CPU(5).Mem(15).Obj()).Obj(),
			},
			pods: []*v1.Pod{
				// CPU: 3, Mem: 3
				MakePod("t3-ns1", "pod1").Phase(v1.PodPending).
					InitContainerRequest(MakeResourceList().CPU(2).Mem(1).Obj()).
					InitContainerRequest(MakeResourceList().CPU(2).Mem(3).Obj()).
					Container(MakeResourceList().CPU(2).Mem(1).Obj()).
					Container(MakeResourceList().CPU(1).Mem(1).Obj()).
					ResourceVersion("2").UID("pod1").Obj(),
				// CPU: 4, Mem: 3
				MakePod("t3-ns2", "pod2").Phase(v1.PodRunning).
					InitContainerRequest(MakeResourceList().CPU(2).Mem(1).Obj()).
					InitContainerRequest(MakeResourceList().CPU(2).Mem(3).Obj()).
					Container(MakeResourceList().CPU(3).Mem(1).Obj()).
					Container(MakeResourceList().CPU(1).Mem(1).Obj()).UID("pod2").Obj(),
			},
			want: []*v1alpha1.ElasticQuota{
				MakeEQ("t3-ns1", "t3-ns1").
					Used(MakeResourceList().CPU(3).Mem(3).Obj()).Obj(),
				MakeEQ("t3-ns2", "t3-ns2").
					Used(MakeResourceList().CPU(4).Mem(3).Obj()).Obj(),
			},
		},
		{
			name: "min and max have the same fields",
			elasticQuotas: []*v1alpha1.ElasticQuota{
				MakeEQ("t4-ns1", "t4-ns1").
					Min(MakeResourceList().CPU(3).Mem(5).Obj()).
					Max(MakeResourceList().CPU(5).Mem(15).Obj()).Obj(),
			},
			pods: []*v1.Pod{},
			want: []*v1alpha1.ElasticQuota{
				MakeEQ("t4-ns1", "t4-ns1").Obj(),
			},
		},
		{
			name: "min and max have the different fields",
			elasticQuotas: []*v1alpha1.ElasticQuota{
				MakeEQ("t5-ns1", "t5-ns1").
					Min(MakeResourceList().CPU(3).Mem(5).GPU(2).Obj()).
					Max(MakeResourceList().CPU(5).Mem(15).Obj()).Obj(),
			},
			pods: []*v1.Pod{},
			want: []*v1alpha1.ElasticQuota{
				MakeEQ("t5-ns1", "t5-ns1").Obj(),
			},
		},
		{
			name: "pod and eq in the different namespaces",
			elasticQuotas: []*v1alpha1.ElasticQuota{
				MakeEQ("t6-ns1", "t6-ns1").
					Min(MakeResourceList().CPU(3).Mem(5).GPU(2).Obj()).
					Max(MakeResourceList().CPU(50).Mem(15).Obj()).Obj(),
				MakeEQ("t6-ns2", "t6-ns2").
					Min(MakeResourceList().CPU(3).Mem(5).GPU(2).Obj()).
					Max(MakeResourceList().CPU(50).Mem(15).Obj()).Obj(),
			},
			pods: []*v1.Pod{
				MakePod("t6-ns3", "pod1").Phase(v1.PodRunning).
					Container(MakeResourceList().CPU(1).Mem(2).GPU(1).Obj()).
					Container(MakeResourceList().CPU(1).Mem(2).Obj()).UID("pod1").Obj(),
			},
			want: []*v1alpha1.ElasticQuota{
				MakeEQ("t6-ns1", "t6-ns1").Obj(),
				MakeEQ("t6-ns2", "t6-ns2").Obj(),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			suit := newPluginTestSuitWithPod(t, nil, nil)
			for _, v := range c.elasticQuotas {
				suit.client.SchedulingV1alpha1().ElasticQuotas(v.Namespace).Create(ctx, v, metav1.CreateOptions{})
			}
			for _, p := range c.pods {
				suit.Handle.ClientSet().CoreV1().Pods(p.Namespace).Create(ctx, p, metav1.CreateOptions{})
			}
			plugin, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
			assert.Nil(t, err)
			p := plugin.(*Plugin)
			ctrl := NewElasticQuotaController(p)
			ctrl.syncElasticQuotaStatusWorker()
			for _, v := range c.want {
				eq, _ := suit.client.SchedulingV1alpha1().ElasticQuotas(v.Namespace).Get(ctx, v.Name, metav1.GetOptions{})
				assert.NotNil(t, eq)
				request, innerErr := extension.GetRequest(eq)
				assert.NoError(t, innerErr)
				if !quotav1.Equals(request, v.Status.Used) {
					err = fmt.Errorf("want %v, got %v,quotaName:%v", v.Status.Used, eq.Status.Used, eq.Name)
					continue
				} else {
					err = nil
					break
				}
			}
			if err != nil {
				t.Errorf("Elastic Quota Test Failed, err: %v", err)
			}
		})
	}
}

func Test_updateElasticQuotaStatusIfChanged(t *testing.T) {
	tests := []struct {
		name    string
		eq      *v1alpha1.ElasticQuota
		summary *core.QuotaInfoSummary
		wantEQ  *v1alpha1.ElasticQuota
	}{
		{
			name: "full sync",
			eq: &v1alpha1.ElasticQuota{
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(4).Mem(200).Obj(),
					Min: MakeResourceList().CPU(2).Mem(100).Obj(),
				},
			},
			summary: &core.QuotaInfoSummary{
				Max:                   MakeResourceList().CPU(4).Mem(200).Obj(),
				Min:                   MakeResourceList().CPU(2).Mem(100).Obj(),
				Used:                  MakeResourceList().CPU(1).Mem(50).Obj(),
				NonPreemptibleUsed:    MakeResourceList().CPU(2).Mem(50).Obj(),
				NonPreemptibleRequest: MakeResourceList().CPU(3).Mem(50).Obj(),
				Request:               MakeResourceList().CPU(4).Mem(50).Obj(),
				Runtime:               MakeResourceList().CPU(5).Mem(50).Obj(),
				ChildRequest:          MakeResourceList().CPU(6).Mem(50).Obj(),
				Allocated:             MakeResourceList().CPU(7).Mem(50).Obj(),
				Guaranteed:            MakeResourceList().CPU(8).Mem(50).Obj(),
			},
			wantEQ: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationNonPreemptibleUsed:    `{"cpu":2,"memory":50}`,
						extension.AnnotationNonPreemptibleRequest: `{"cpu":3,"memory":50}`,
						extension.AnnotationRequest:               `{"cpu":4,"memory":50}`,
						extension.AnnotationRuntime:               `{"cpu":5,"memory":50}`,
						extension.AnnotationChildRequest:          `{"cpu":6,"memory":50}`,
						extension.AnnotationAllocated:             `{"cpu":7,"memory":50}`,
						extension.AnnotationGuaranteed:            `{"cpu":8,"memory":50}`,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(4).Mem(200).Obj(),
					Min: MakeResourceList().CPU(2).Mem(100).Obj(),
				},
				Status: v1alpha1.ElasticQuotaStatus{
					Used: MakeResourceList().CPU(1).Mem(50).Obj(),
				},
			},
		},
		{
			name: "diff sync",
			eq: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationNonPreemptibleUsed:    `{"cpu":2,"memory":50}`,
						extension.AnnotationNonPreemptibleRequest: `{"cpu":3,"memory":50}`,
						extension.AnnotationRequest:               `{"cpu":4,"memory":50}`,
						extension.AnnotationRuntime:               `{"cpu":5,"memory":50}`,
						extension.AnnotationChildRequest:          `{"cpu":6,"memory":50}`,
						extension.AnnotationAllocated:             `{"cpu":7,"memory":50}`,
						extension.AnnotationGuaranteed:            `{"cpu":8,"memory":50}`,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(4).Mem(200).Obj(),
					Min: MakeResourceList().CPU(2).Mem(100).Obj(),
				},
				Status: v1alpha1.ElasticQuotaStatus{
					Used: MakeResourceList().CPU(1).Mem(50).Obj(),
				},
			},
			summary: &core.QuotaInfoSummary{
				Max:                   MakeResourceList().CPU(4).Mem(200).Obj(),
				Min:                   MakeResourceList().CPU(2).Mem(100).Obj(),
				Used:                  MakeResourceList().CPU(1).Mem(50).Obj(),
				NonPreemptibleUsed:    MakeResourceList().CPU(2).Mem(50).Obj(),
				NonPreemptibleRequest: MakeResourceList().CPU(3).Mem(50).Obj(),
				Request:               MakeResourceList().CPU(4).Mem(50).Obj(),
				Runtime:               MakeResourceList().CPU(5).Mem(50).Obj(),
				ChildRequest:          MakeResourceList().CPU(6).Mem(50).Obj(),
				Allocated:             MakeResourceList().CPU(7).Mem(50).Obj(),
				Guaranteed:            MakeResourceList().CPU(66666666).Mem(50).Obj(),
			},
			wantEQ: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationNonPreemptibleUsed:    `{"cpu":2,"memory":50}`,
						extension.AnnotationNonPreemptibleRequest: `{"cpu":3,"memory":50}`,
						extension.AnnotationRequest:               `{"cpu":4,"memory":50}`,
						extension.AnnotationRuntime:               `{"cpu":5,"memory":50}`,
						extension.AnnotationChildRequest:          `{"cpu":6,"memory":50}`,
						extension.AnnotationAllocated:             `{"cpu":7,"memory":50}`,
						extension.AnnotationGuaranteed:            `{"cpu":66666666,"memory":50}`,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(4).Mem(200).Obj(),
					Min: MakeResourceList().CPU(2).Mem(100).Obj(),
				},
				Status: v1alpha1.ElasticQuotaStatus{
					Used: MakeResourceList().CPU(1).Mem(50).Obj(),
				},
			},
		},
		{
			name: "no sync",
			eq: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationNonPreemptibleUsed:    `{"cpu":2,"memory":50}`,
						extension.AnnotationNonPreemptibleRequest: `{"cpu":3,"memory":50}`,
						extension.AnnotationRequest:               `{"cpu":4,"memory":50}`,
						extension.AnnotationRuntime:               `{"cpu":5,"memory":50}`,
						extension.AnnotationChildRequest:          `{"cpu":6,"memory":50}`,
						extension.AnnotationAllocated:             `{"cpu":7,"memory":50}`,
						extension.AnnotationGuaranteed:            `{"cpu":8,"memory":50}`,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(4).Mem(200).Obj(),
					Min: MakeResourceList().CPU(2).Mem(100).Obj(),
				},
				Status: v1alpha1.ElasticQuotaStatus{
					Used: MakeResourceList().CPU(1).Mem(50).Obj(),
				},
			},
			summary: &core.QuotaInfoSummary{
				Max:                   MakeResourceList().CPU(4).Mem(200).Obj(),
				Min:                   MakeResourceList().CPU(2).Mem(100).Obj(),
				Used:                  MakeResourceList().CPU(1).Mem(50).Obj(),
				NonPreemptibleUsed:    MakeResourceList().CPU(2).Mem(50).Obj(),
				NonPreemptibleRequest: MakeResourceList().CPU(3).Mem(50).Obj(),
				Request:               MakeResourceList().CPU(4).Mem(50).Obj(),
				Runtime:               MakeResourceList().CPU(5).Mem(50).Obj(),
				ChildRequest:          MakeResourceList().CPU(6).Mem(50).Obj(),
				Allocated:             MakeResourceList().CPU(7).Mem(50).Obj(),
				Guaranteed:            MakeResourceList().CPU(8).Mem(50).Obj(),
			},
			wantEQ: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newEQ, err := updateElasticQuotaStatusIfChanged(tt.eq, tt.summary, true)
			assert.NoError(t, err)
			if tt.wantEQ == nil {
				assert.Nil(t, newEQ)
				return
			}
			assert.Equal(t, tt.wantEQ.Spec, newEQ.Spec)
			assert.Equal(t, tt.wantEQ.Status, newEQ.Status)
			for k, v := range tt.wantEQ.Annotations {
				var expect v1.ResourceList
				assert.NoError(t, json.Unmarshal([]byte(v), &expect))
				var got v1.ResourceList
				v = newEQ.Annotations[k]
				assert.NoError(t, json.Unmarshal([]byte(v), &got))
				assert.Equal(t, expect, got)
			}
		})
	}
}

func Test_syncElasticQuotaMetrics(t *testing.T) {
	eq := &v1alpha1.ElasticQuota{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"quota.scheduling.koordinator.sh/is-root": "true",
			},
			Annotations: map[string]string{
				"quota.scheduling.koordinator.sh/unschedulable-resource": `{"cpu":"4","memory":"8"}`,
			},
		},
		Spec: v1alpha1.ElasticQuotaSpec{
			Max: MakeResourceList().CPU(4).Mem(200).Obj(),
			Min: MakeResourceList().CPU(2).Mem(100).Obj(),
		},
	}
	summary := &core.QuotaInfoSummary{
		Name:                  "test-eq",
		ParentName:            "root",
		IsParent:              true,
		Tree:                  "tree-1",
		Max:                   MakeResourceList().CPU(4).Mem(200).Obj(),
		Min:                   MakeResourceList().CPU(2).Mem(100).Obj(),
		Used:                  MakeResourceList().CPU(1).Mem(50).Obj(),
		NonPreemptibleUsed:    MakeResourceList().CPU(2).Mem(50).Obj(),
		NonPreemptibleRequest: MakeResourceList().CPU(3).Mem(50).Obj(),
		Request:               MakeResourceList().CPU(4).Mem(50).Obj(),
		Runtime:               MakeResourceList().CPU(5).Mem(50).Obj(),
		ChildRequest:          MakeResourceList().CPU(6).Mem(50).Obj(),
		Allocated:             MakeResourceList().CPU(7).Mem(50).Obj(),
		Guaranteed:            MakeResourceList().CPU(8).Mem(50).Obj(),
	}
	syncElasticQuotaMetrics(eq, summary)
	metricsCh := make(chan prometheus.Metric, 10)
	go func() {
		ElasticQuotaSpecMetric.Collect(metricsCh)
		ElasticQuotaStatusMetric.Collect(metricsCh)
		close(metricsCh)
	}()
	var ms []prometheus.Metric
loopChan:
	for {
		select {
		case metric, ok := <-metricsCh:
			if !ok {
				break loopChan
			}
			ms = append(ms, metric)
		}
	}

	var dtoMetrics []*dto.Metric
	for _, v := range ms {
		m := dto.Metric{}
		assert.NoError(t, v.Write(&m))
		dtoMetrics = append(dtoMetrics, &m)
	}
	sort.Slice(dtoMetrics, func(i, j int) bool {
		return dtoMetrics[i].String() < dtoMetrics[j].String()
	})
	expect := []string{
		`{"label":[{"name":"field","value":"allocated"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":7000}}`,
		`{"label":[{"name":"field","value":"allocated"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":50}}`,
		`{"label":[{"name":"field","value":"child-request"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":6000}}`,
		`{"label":[{"name":"field","value":"child-request"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":50}}`,
		`{"label":[{"name":"field","value":"guaranteed"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":8000}}`,
		`{"label":[{"name":"field","value":"guaranteed"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":50}}`,
		`{"label":[{"name":"field","value":"max"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":4000}}`,
		`{"label":[{"name":"field","value":"max"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":200}}`,
		`{"label":[{"name":"field","value":"min"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":2000}}`,
		`{"label":[{"name":"field","value":"min"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":100}}`,
		`{"label":[{"name":"field","value":"non-preemptible-request"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":3000}}`,
		`{"label":[{"name":"field","value":"non-preemptible-request"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":50}}`,
		`{"label":[{"name":"field","value":"non-preemptible-used"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":2000}}`,
		`{"label":[{"name":"field","value":"non-preemptible-used"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":50}}`,
		`{"label":[{"name":"field","value":"request"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":4000}}`,
		`{"label":[{"name":"field","value":"request"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":50}}`,
		`{"label":[{"name":"field","value":"runtime"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":5000}}`,
		`{"label":[{"name":"field","value":"runtime"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":50}}`,
		`{"label":[{"name":"field","value":"unschedulable-resource"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":4000}}`,
		`{"label":[{"name":"field","value":"unschedulable-resource"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":8}}`,
		`{"label":[{"name":"field","value":"used"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"cpu"},{"name":"tree","value":"tree-1"}],"gauge":{"value":1000}}`,
		`{"label":[{"name":"field","value":"used"},{"name":"is_parent","value":"true"},{"name":"name","value":"test-eq"},{"name":"parent","value":"root"},{"name":"resource","value":"memory"},{"name":"tree","value":"tree-1"}],"gauge":{"value":50}}`,
	}
	assert.Equal(t, len(expect), len(dtoMetrics))
	for i := range dtoMetrics {
		v := dtoMetrics[i]
		jsonStrBytes, _ := json.Marshal(v)
		assert.Equal(t, expect[i], string(jsonStrBytes))
	}
}

type eqWrapper struct{ *v1alpha1.ElasticQuota }

func MakeEQ(namespace, name string) *eqWrapper {
	eq := &v1alpha1.ElasticQuota{
		TypeMeta: metav1.TypeMeta{Kind: "ElasticQuota", APIVersion: "scheduling.sigs.k8s.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: make(map[string]string),
		},
	}
	return &eqWrapper{eq}
}

func (e *eqWrapper) Min(min v1.ResourceList) *eqWrapper {
	e.ElasticQuota.Spec.Min = min
	return e
}

func (e *eqWrapper) Max(max v1.ResourceList) *eqWrapper {
	e.ElasticQuota.Spec.Max = max
	return e
}

func (e *eqWrapper) Used(used v1.ResourceList) *eqWrapper {
	e.ElasticQuota.Status.Used = used
	return e
}

func (e *eqWrapper) Annotations(annotations map[string]string) *eqWrapper {
	for k, v := range annotations {
		e.ElasticQuota.Annotations[k] = v
	}
	return e
}

func (e *eqWrapper) Obj() *v1alpha1.ElasticQuota {
	return e.ElasticQuota
}

type podWrapper struct{ *v1.Pod }

func MakePod(namespace, name string) *podWrapper {
	pod := testing2.MakePod().Namespace(namespace).Name(name).Obj()

	return &podWrapper{pod}
}

func (p *podWrapper) UID(id string) *podWrapper {
	p.SetUID(types.UID(id))
	return p
}

func (p *podWrapper) Label(string1, string2 string) *podWrapper {
	if p.Labels == nil {
		p.Labels = make(map[string]string)
	}
	p.Labels[string1] = string2
	return p
}

func (p *podWrapper) Phase(phase v1.PodPhase) *podWrapper {
	p.Pod.Status.Phase = phase
	return p
}

func (p *podWrapper) Container(request v1.ResourceList) *podWrapper {
	p.Pod.Spec.Containers = append(p.Pod.Spec.Containers, v1.Container{
		Name:  fmt.Sprintf("con%d", len(p.Pod.Spec.Containers)),
		Image: "image",
		Resources: v1.ResourceRequirements{
			Requests: request,
		},
	})
	return p
}

func (p *podWrapper) ResourceVersion(version string) *podWrapper {
	p.SetResourceVersion(version)
	return p
}

func (p *podWrapper) InitContainerRequest(request v1.ResourceList) *podWrapper {
	p.Pod.Spec.InitContainers = append(p.Pod.Spec.InitContainers, v1.Container{
		Name:  fmt.Sprintf("con%d", len(p.Pod.Spec.Containers)),
		Image: "image",
		Resources: v1.ResourceRequirements{
			Requests: request,
		},
	})
	return p
}

func (p *podWrapper) Obj() *v1.Pod {
	return p.Pod
}

type resourceWrapper struct{ v1.ResourceList }

func MakeResourceList() *resourceWrapper {
	return &resourceWrapper{v1.ResourceList{}}
}

func (r *resourceWrapper) CPU(val int64) *resourceWrapper {
	r.ResourceList[v1.ResourceCPU] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) Mem(val int64) *resourceWrapper {
	r.ResourceList[v1.ResourceMemory] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) GPU(val int64) *resourceWrapper {
	r.ResourceList["nvidia.com/gpu"] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) Obj() v1.ResourceList {
	return r.ResourceList
}
