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

package quotaevaluate

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
)

// newTestEvaluator returns an Evaluator backed by a fake client preloaded with
// the given quotas. The same fake client is reused as both the cached client
// and the apiReader since the fake has no informer cache to skip.
func newTestEvaluator(quotas ...*v1alpha1.ElasticQuota) (Evaluator, client.Client) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	objs := make([]client.Object, 0, len(quotas))
	for _, q := range quotas {
		objs = append(objs, q)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return NewQuotaEvaluator(NewQuotaAccessor(c, c), 16, make(chan struct{})), c
}

func TestCheckRequest(t *testing.T) {
	e := &quotaEvaluator{}

	tests := []struct {
		name        string
		quota       *v1alpha1.ElasticQuota
		ctx         *quotaCheckContext
		attr        *Attributes
		expectErr   bool
		errContains string
		expectUsed  corev1.ResourceList
		expectDelta corev1.ResourceList
	}{
		{
			name: "successful request updates used and delta",
			quota: elasticquota.MakeQuota("test").Namespace("ns").Max(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).Obj(),
			ctx: &quotaCheckContext{
				used:      corev1.ResourceList{},
				admission: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20"), corev1.ResourceMemory: resource.MustParse("60Gi")},
				delta:     corev1.ResourceList{},
				names:     sets.New(corev1.ResourceCPU, corev1.ResourceMemory),
			},
			attr: &Attributes{
				Operation: admissionv1.Create,
				Pod: elasticquota.MakePod("ns", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					}).Obj(),
			},
			expectErr:   false,
			expectUsed:  corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
			expectDelta: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
		},
		{
			name: "exceeded quota returns error",
			quota: elasticquota.MakeQuota("test").Namespace("ns").Max(
				corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				}).Obj(),
			ctx: &quotaCheckContext{
				used:      corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("3")},
				admission: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				delta:     corev1.ResourceList{},
				names:     sets.New(corev1.ResourceCPU),
			},
			attr: &Attributes{
				Operation: admissionv1.Create,
				Pod: elasticquota.MakePod("ns", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("2"),
					}).Obj(),
			},
			expectErr:   true,
			errContains: "exceeded quota",
			expectUsed:  corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("3")},
			expectDelta: corev1.ResourceList{},
		},
		{
			name: "non-create operation is no-op",
			quota: elasticquota.MakeQuota("test").Namespace("ns").Max(
				corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20")}).Obj(),
			ctx: &quotaCheckContext{
				used:      corev1.ResourceList{},
				admission: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20")},
				delta:     corev1.ResourceList{},
				names:     sets.New(corev1.ResourceCPU),
			},
			attr: &Attributes{
				Operation: admissionv1.Update,
				Pod: elasticquota.MakePod("ns", "pod1").Container(
					corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}).Obj(),
			},
			expectErr:   false,
			expectUsed:  corev1.ResourceList{},
			expectDelta: corev1.ResourceList{},
		},
		{
			name: "only quota-managed resources are counted",
			quota: elasticquota.MakeQuota("test").Namespace("ns").Max(
				corev1.ResourceList{
					extension.ResourceNvidiaGPU: resource.MustParse("4"),
				}).Obj(),
			ctx: &quotaCheckContext{
				used:      corev1.ResourceList{},
				admission: corev1.ResourceList{extension.ResourceNvidiaGPU: resource.MustParse("4")},
				delta:     corev1.ResourceList{},
				names:     sets.New(extension.ResourceNvidiaGPU),
			},
			attr: &Attributes{
				Operation: admissionv1.Create,
				Pod: elasticquota.MakePod("ns", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:          resource.MustParse("2"),
						corev1.ResourceMemory:       resource.MustParse("4Gi"),
						extension.ResourceNvidiaGPU: resource.MustParse("1"),
					}).Obj(),
			},
			expectErr:   false,
			expectUsed:  corev1.ResourceList{extension.ResourceNvidiaGPU: resource.MustParse("1")},
			expectDelta: corev1.ResourceList{extension.ResourceNvidiaGPU: resource.MustParse("1")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.checkRequest(tt.quota, tt.attr, tt.ctx)
			if tt.expectErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectUsed, tt.ctx.used)
			assert.Equal(t, tt.expectDelta, tt.ctx.delta)
		})
	}
}

// retryMockAccessor simulates update failure on first attempt then success on retry.
type retryMockAccessor struct {
	firstQuota  *v1alpha1.ElasticQuota
	retryQuota  *v1alpha1.ElasticQuota
	failFirst   bool
	updateCalls int
}

func (m *retryMockAccessor) GetQuota(key string) (*v1alpha1.ElasticQuota, error) {
	return m.retryQuota.DeepCopy(), nil
}

func (m *retryMockAccessor) UpdateQuotaStatus(newQuota *v1alpha1.ElasticQuota, _ time.Duration) error {
	m.updateCalls++
	if m.failFirst && m.updateCalls == 1 {
		return fmt.Errorf("conflict")
	}
	return nil
}

func TestCheckQuotaFastRetry(t *testing.T) {
	tests := []struct {
		name             string
		quota            *v1alpha1.ElasticQuota
		retryQuota       *v1alpha1.ElasticQuota
		attrs            []*Attributes
		expectAllSuccess bool
	}{
		{
			name: "fast retry succeeds when quota has capacity",
			quota: elasticquota.MakeQuota("test").Namespace("ns").Max(
				corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("20"),
				}).ChildRequest(
				corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("10"),
				}).Obj(),
			retryQuota: elasticquota.MakeQuota("test").Namespace("ns").Max(
				corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("20"),
				}).ChildRequest(
				corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("12"),
				}).Obj(),
			attrs: []*Attributes{
				{
					QuotaNamespace: "ns",
					QuotaName:      "test",
					Operation:      admissionv1.Create,
					Pod: elasticquota.MakePod("ns", "pod1").Container(
						corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}).Obj(),
				},
				{
					QuotaNamespace: "ns",
					QuotaName:      "test",
					Operation:      admissionv1.Create,
					Pod: elasticquota.MakePod("ns", "pod2").Container(
						corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("3")}).Obj(),
				},
			},
			expectAllSuccess: true,
		},
		{
			name: "fast retry falls back to full evaluation when exceeds",
			quota: elasticquota.MakeQuota("test").Namespace("ns").Max(
				corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("20"),
				}).ChildRequest(
				corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("10"),
				}).Obj(),
			retryQuota: elasticquota.MakeQuota("test").Namespace("ns").Max(
				corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("20"),
				}).ChildRequest(
				corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("18"),
				}).Obj(),
			attrs: []*Attributes{
				{
					QuotaNamespace: "ns",
					QuotaName:      "test",
					Operation:      admissionv1.Create,
					Pod: elasticquota.MakePod("ns", "pod1").Container(
						corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("5")}).Obj(),
				},
			},
			expectAllSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accessor := &retryMockAccessor{
				firstQuota: tt.quota,
				retryQuota: tt.retryQuota,
				failFirst:  true,
			}
			e := &quotaEvaluator{quotaAccessor: accessor}

			waiters := make([]*admissionWaiter, len(tt.attrs))
			for i, attr := range tt.attrs {
				waiters[i] = newAdmissionWaiter(attr)
			}

			e.checkQuota(tt.quota, waiters, QuotaUpdateMaxRetries, nil)

			allSuccess := true
			for _, w := range waiters {
				if w.result != nil {
					allSuccess = false
					break
				}
			}
			assert.Equal(t, tt.expectAllSuccess, allSuccess)
		})
	}
}

func TestEvaluate(t *testing.T) {
	testCases := []struct {
		name        string
		quota       *v1alpha1.ElasticQuota
		attribute   *Attributes
		expectError bool
		errMessage  string
		expectUsed  corev1.ResourceList
	}{
		{
			name: "normal case",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					}).Obj(),
			},
			expectError: false,
			expectUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
		{
			name: "cpu exceed",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).ChildRequest(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("19"),
					corev1.ResourceMemory: resource.MustParse("50Gi"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					}).Obj(),
			},
			expectError: true,
			errMessage:  "exceeded quota: ns1/test1, requested: cpu=2, used: cpu=19, limited: cpu=20",
			expectUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
		{
			name: "admission allow",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).ChildRequest(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("19"),
					corev1.ResourceMemory: resource.MustParse("50Gi"),
				}).Admission(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("30"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					}).Obj(),
			},
			expectError: false,
			expectUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("21"),
				corev1.ResourceMemory: resource.MustParse("54Gi"),
			},
		},
		{
			name: "gpu normal case",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					extension.ResourceNvidiaGPU: resource.MustParse("2"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:          resource.MustParse("2"),
						corev1.ResourceMemory:       resource.MustParse("4Gi"),
						extension.ResourceNvidiaGPU: resource.MustParse("2"),
					}).Obj(),
			},
			expectError: false,
			expectUsed: corev1.ResourceList{
				extension.ResourceNvidiaGPU: resource.MustParse("2"),
			},
		},
		{
			name: "gpu exceed",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					extension.ResourceNvidiaGPU: resource.MustParse("2"),
				}).ChildRequest(
				corev1.ResourceList{
					extension.ResourceNvidiaGPU: resource.MustParse("2"),
				}).Obj(),
			attribute: &Attributes{
				QuotaNamespace: "ns1",
				QuotaName:      "test1",
				Operation:      admissionv1.Create,
				Pod: elasticquota.MakePod("ns1", "pod1").Container(
					corev1.ResourceList{
						corev1.ResourceCPU:          resource.MustParse("2"),
						corev1.ResourceMemory:       resource.MustParse("4Gi"),
						extension.ResourceNvidiaGPU: resource.MustParse("2"),
					}).Obj(),
			},
			expectError: true,
			errMessage:  "exceeded quota: ns1/test1, requested: nvidia.com/gpu=2, used: nvidia.com/gpu=2, limited: nvidia.com/gpu=2",
			expectUsed: corev1.ResourceList{
				extension.ResourceNvidiaGPU: resource.MustParse("2"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			evaluator, c := newTestEvaluator(tc.quota)

			err := evaluator.Evaluate(tc.attribute)
			if tc.expectError {
				assert.Error(t, err)
				assert.Equal(t, tc.errMessage, err.Error())
				t.Logf("expect err: %v", err)
			} else {
				assert.NoError(t, err)

				newQuota := &v1alpha1.ElasticQuota{}
				err = c.Get(context.TODO(), types.NamespacedName{
					Namespace: tc.quota.Namespace,
					Name:      tc.quota.Name,
				}, newQuota)
				assert.NoError(t, err)
				newUsage, err := extension.GetChildRequest(newQuota)
				assert.NoError(t, err)
				t.Logf("expec %v, got %v", util.DumpJSON(tc.expectUsed), util.DumpJSON(newUsage))
				assert.Equal(t, tc.expectUsed, newUsage)
			}
		})
	}
}

func TestEvaluateSequential(t *testing.T) {
	type step struct {
		req         corev1.ResourceList
		expectErr   string // substring; empty means success
	}
	tests := []struct {
		name            string
		quota           *v1alpha1.ElasticQuota
		steps           []step
		expectFinalUsed corev1.ResourceList
	}{
		{
			name: "multiple pods accumulate usage",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				}).Obj(),
			steps: []step{
				{req: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("5"), corev1.ResourceMemory: resource.MustParse("10Gi")}},
				{req: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("5"), corev1.ResourceMemory: resource.MustParse("10Gi")}},
			},
			expectFinalUsed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
		},
		{
			name: "second pod exceeds accumulated quota",
			quota: elasticquota.MakeQuota("test1").Namespace("ns1").Max(
				corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10")}).ChildRequest(
				corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")}).Obj(),
			steps: []step{
				{req: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}},                  // 8+1=9 <=10
				{req: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("3")}, expectErr: "exceeded quota"}, // 9+3=12 >10
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator, c := newTestEvaluator(tt.quota)

			for i, s := range tt.steps {
				err := evaluator.Evaluate(&Attributes{
					QuotaNamespace: tt.quota.Namespace,
					QuotaName:      tt.quota.Name,
					Operation:      admissionv1.Create,
					Pod:            elasticquota.MakePod(tt.quota.Namespace, fmt.Sprintf("pod%d", i+1)).Container(s.req).Obj(),
				})
				if s.expectErr != "" {
					assert.Error(t, err)
					assert.Contains(t, err.Error(), s.expectErr)
				} else {
					assert.NoError(t, err)
				}
			}

			if tt.expectFinalUsed == nil {
				return
			}
			got := &v1alpha1.ElasticQuota{}
			assert.NoError(t, c.Get(context.TODO(), types.NamespacedName{
				Namespace: tt.quota.Namespace,
				Name:      tt.quota.Name,
			}, got))
			used, err := extension.GetChildRequest(got)
			assert.NoError(t, err)
			for k, v := range tt.expectFinalUsed {
				assert.Equal(t, v, used[k], "resource %s", k)
			}
		})
	}
}

func TestRetriesForBatch(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{"empty batch falls back to min", 0, quotaUpdateMinRetries},
		{"single pod equals legacy hardcoded value", 1, quotaUpdateMinRetries},
		{"two pods earn one extra retry", 2, quotaUpdateMinRetries + 1},
		{"three pods still in log2=1 bucket", 3, quotaUpdateMinRetries + 1},
		{"four pods earn two extra retries", 4, quotaUpdateMinRetries + 2},
		{"eight pods earn three extra retries", 8, quotaUpdateMinRetries + 3},
		{"capped at QuotaUpdateMaxRetries", 1 << 20, QuotaUpdateMaxRetries},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, retriesForBatch(tc.n))
		})
	}

	t.Run("respects QuotaUpdateMaxRetries override", func(t *testing.T) {
		orig := QuotaUpdateMaxRetries
		t.Cleanup(func() { QuotaUpdateMaxRetries = orig })

		QuotaUpdateMaxRetries = 5
		assert.Equal(t, 5, retriesForBatch(1<<10))
		assert.Equal(t, quotaUpdateMinRetries, retriesForBatch(1))

		// max lower than min collapses to min
		QuotaUpdateMaxRetries = 1
		assert.Equal(t, quotaUpdateMinRetries, retriesForBatch(1<<10))
	})
}

func TestJitterForNextUpdate(t *testing.T) {
	const (
		defR = defaultQuotaUpdateRetryJitter
		defT = defaultQuotaUpdateMaxTotalJitter
	)
	tests := []struct {
		name             string
		retryJitter      time.Duration
		maxTotalJitter   time.Duration
		spent            time.Duration
		remainingRetries int
		want             time.Duration
	}{
		{"happy path", defR, defT, 0, 1, defR},
		{"last attempt", defR, defT, 0, 0, 0},
		{"budget exhausted", defR, defT, defT - defR + 1, 5, 0},
		{"retry knob zero disables", 0, defT, 0, 5, 0},
		{"retry knob negative disables", -time.Millisecond, defT, 0, 5, 0},
		{"total knob zero disables", defR, 0, 0, 5, 0},
		{"total knob negative disables", defR, -time.Second, 0, 5, 0},
		{"custom fits", 50 * time.Millisecond, 75 * time.Millisecond, 0, 5, 50 * time.Millisecond},
		{"custom overshoot", 50 * time.Millisecond, 75 * time.Millisecond, 50 * time.Millisecond, 5, 0},
	}

	e := &quotaEvaluator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origR, origT := QuotaUpdateRetryJitter, QuotaUpdateMaxTotalJitter
			t.Cleanup(func() {
				QuotaUpdateRetryJitter, QuotaUpdateMaxTotalJitter = origR, origT
			})
			QuotaUpdateRetryJitter = tt.retryJitter
			QuotaUpdateMaxTotalJitter = tt.maxTotalJitter

			ctx := &quotaCheckContext{totalJitterSpent: tt.spent}
			assert.Equal(t, tt.want, e.jitterForNextUpdate(ctx, tt.remainingRetries))
		})
	}
}
