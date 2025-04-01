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
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schetesting "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"

	_ "net/http/pprof"
)

type benchCase struct {
	name              string
	numCustomLimiters int
	numPodsPerQuota   int
	numQuotasPerLevel []int
	prepareFn         quotaOptFn
	testFn            func(b *testing.B, env *benchEnv, i int)
	checkFn           func(b *testing.B, env *benchEnv, n int64)
	clearFn           quotaOptFn
}

type benchEnv struct {
	plugin *Plugin
	gqm    *core.GroupQuotaManager
	quotas map[string]*v1alpha1.ElasticQuota
}

type quotaOptFn func(env *benchEnv)

func BenchmarkCustomLimitersUpdateQuota(b *testing.B) {
	core.RegisterCustomLimiterFactory(core.MockLimiterFactoryKey, core.NewMockLimiter)
	envCache = make(map[string]*benchEnv)
	// test cases: update custom-limit for level-0 quota
	testCases := []benchCase{
		{
			name:              "update custom state: 3 custom-limiters, 1k leaf quotas, 4 pods per leaf quota",
			numCustomLimiters: 3,
			numPodsPerQuota:   4,
			numQuotasPerLevel: []int{1, 1000, 1, 1},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				addQuotaCustomLimitFn(b, env, i, 3, 0)
				deleteQuotaCustomLimitFn(b, env, 3, 0)
			},
		},
		{
			name:              "update custom state: 3 custom-limiters, 3k leaf quotas, 4 pods per leaf quota",
			numCustomLimiters: 3,
			numPodsPerQuota:   4,
			numQuotasPerLevel: []int{1, 1000, 1, 3},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				addQuotaCustomLimitFn(b, env, i, 3, 0)
				deleteQuotaCustomLimitFn(b, env, 3, 0)
			},
		},
		{
			name:              "update custom state: 3 custom-limiters, 6k leaf quotas, 4 pods per leaf quota",
			numCustomLimiters: 3,
			numPodsPerQuota:   4,
			numQuotasPerLevel: []int{1, 2000, 1, 3},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				addQuotaCustomLimitFn(b, env, i, 3, 0)
				deleteQuotaCustomLimitFn(b, env, 3, 0)
			},
		},
		{
			name:              "update custom state: 3 custom-limiters, 1.2w leaf quotas, 4 pods per leaf quota",
			numCustomLimiters: 3,
			numPodsPerQuota:   4,
			numQuotasPerLevel: []int{1, 2000, 2, 3},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				addQuotaCustomLimitFn(b, env, i, 3, 0)
				deleteQuotaCustomLimitFn(b, env, 3, 0)
			},
		},
	}
	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkBenchCase(b, tc)
		})
	}
}

func BenchmarkCustomLimitersAddPod(b *testing.B) {
	core.RegisterCustomLimiterFactory(core.MockLimiterFactoryKey, core.NewMockLimiter)
	envCache = make(map[string]*benchEnv)
	// test cases
	testCases := []benchCase{
		{
			name:              "add pod: 0 custom-limiters, 4 level quotas",
			numCustomLimiters: 0,
			numQuotasPerLevel: []int{1, 1, 1, 1},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				addPodFn(b, env, i, 3, 3)
			},
			checkFn: func(b *testing.B, env *benchEnv, n int64) {
				checkUsedFn(b, env, 3, n)
			},
		},
		{
			name:              "add pod: 3 custom-limiters, 4 level quotas (custom-used should be updated)",
			numCustomLimiters: 3,
			numQuotasPerLevel: []int{1, 1, 1, 1},
			prepareFn: func(env *benchEnv) {
				addQuotaCustomLimitFn(b, env, 0, 3, 3)
			},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				addPodFn(b, env, i, 3, 3)
			},
			checkFn: func(b *testing.B, env *benchEnv, n int64) {
				checkCustomUsedFn(b, env, n)
			},
		},
	}
	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkBenchCase(b, tc)
		})
	}
}

func BenchmarkCustomLimitersCheckPod(b *testing.B) {
	core.RegisterCustomLimiterFactory(core.MockLimiterFactoryKey, core.NewMockLimiter)
	envCache = make(map[string]*benchEnv)
	// test cases: check pod
	testCases := []benchCase{
		{
			name:              "check pod: 0 custom-limiters, 4 level quotas, pass",
			numCustomLimiters: 0,
			numQuotasPerLevel: []int{1, 1, 1, 1},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				checkPodFn(b, env, i, 3, 3,
					func(b *testing.B, status *framework.Status) {
						if status != nil && !status.IsSuccess() {
							b.Fatalf("failed to check custom-limit, unexpected status: %+v", status)
						}
					})
			},
		},
		{
			name:              "check pod: 1 custom-limiters, 4 levels, custom-limit not reached, pass",
			numCustomLimiters: 1,
			numQuotasPerLevel: []int{1, 1, 1, 1},
			prepareFn: func(env *benchEnv) {
				addQuotaCustomLimitFn(b, env, 10, 1, 0)
			},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				checkPodFn(b, env, i, 1, 3,
					func(b *testing.B, status *framework.Status) {
						if status != nil && !status.IsSuccess() {
							b.Fatalf("failed to check custom-limit, unexpected status: %+v", status)
						}
					})
			},
		},
		{
			name:              "check pod: 3 custom-limiters, 4 levels, custom-limit not reached, pass",
			numCustomLimiters: 3,
			numQuotasPerLevel: []int{1, 1, 1, 1},
			prepareFn: func(env *benchEnv) {
				addQuotaCustomLimitFn(b, env, 10, 3, 0)
			},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				checkPodFn(b, env, i, 3, 3,
					func(b *testing.B, status *framework.Status) {
						if status != nil && !status.IsSuccess() {
							b.Fatalf("failed to check custom-limit, unexpected status: %+v", status)
						}
					})
			},
		},
		{
			name:              "check pod: 3 custom-limiters, 4 levels, custom-limit reached, reject",
			numCustomLimiters: 3,
			numQuotasPerLevel: []int{1, 1, 1, 1},
			prepareFn: func(env *benchEnv) {
				// add custom-limit <cpu:0,memory:0> to level-0 quota
				addQuotaCustomLimitFn(b, env, 0, 3, 0)
			},
			testFn: func(b *testing.B, env *benchEnv, i int) {
				checkPodFn(b, env, i, 3, 3,
					func(b *testing.B, status *framework.Status) {
						if status != nil && !status.IsUnschedulable() {
							b.Fatalf("failed to check custom-limit, unexpected status: %+v", status)
						}
					})
			},
		},
	}
	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkBenchCase(b, tc)
		})
	}
}

func benchmarkBenchCase(b *testing.B, tc benchCase) {
	// prepare
	startTime := time.Now()
	env, err := getTestEnvFn(tc.numCustomLimiters, tc.numQuotasPerLevel, tc.numPodsPerQuota)
	if err != nil {
		b.Error(err.Error())
		b.FailNow()
	}
	if tc.prepareFn != nil {
		tc.prepareFn(env)
	}
	b.Logf("[prepare] elapsed time is %s", time.Since(startTime))

	// reset timer for this benchmark case
	b.ResetTimer()
	startTime = time.Now()
	for i := 0; i < b.N; i++ {
		tc.testFn(b, env, i)
	}
	duration := time.Since(startTime)
	qps := math.Round(float64(b.N) / duration.Seconds())
	b.Logf("[test] total elapsed time is %s, average time is %s, QPS: %v, count: %d",
		duration, duration/time.Duration(b.N), qps, b.N)

	// check
	if tc.checkFn != nil {
		tc.checkFn(b, env, int64(b.N))
	}

	// clear
	if tc.clearFn != nil {
		startTime = time.Now()
		tc.clearFn(env)
		b.Logf("[clear] elapsed time is %s", time.Since(startTime))
	}
}

func getTargetQuotaName(targetLevel int) string {
	var targetQuotaName string
	for i := 0; i <= targetLevel; i++ {
		targetQuotaName += "_0"
	}
	return targetQuotaName
}

var envCache map[string]*benchEnv

func getTestEnvFn(numCustomLimiters int, numQuotasPerLevel []int, numPodsPerQuota int) (*benchEnv, error) {
	envKey := fmt.Sprintf("%d_%v", numCustomLimiters, numQuotasPerLevel)
	if env, ok := envCache[envKey]; ok {
		return env, nil
	}
	customLimiters := make(map[string]config.CustomLimiterConf)
	for i := 0; i < numCustomLimiters; i++ {
		key := fmt.Sprintf("mock%d", i)
		customLimiters[key] = config.CustomLimiterConf{
			FactoryKey:  core.MockLimiterFactoryKey,
			FactoryArgs: fmt.Sprintf(`{"labelSelector":"%s=true"}`, key),
		}
	}
	// test suit
	suit, err := newPluginTestSuitForBenchmark(nil,
		func(elasticQuotaArgs *config.ElasticQuotaArgs) {
			elasticQuotaArgs.EnableRuntimeQuota = false
			elasticQuotaArgs.CustomLimiters = customLimiters
			setLoglevel("1")
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create test-suit for benchmark, err=%v", err)
	}
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	if err != nil {
		return nil, err
	}
	plugin := p.(*Plugin)
	gqm := plugin.groupQuotaManager
	quotas := make(map[string]*v1alpha1.ElasticQuota)
	err = createBenchmarkQuotasRecursive(plugin, "", 0, numPodsPerQuota, numQuotasPerLevel, quotas)
	if err != nil {
		return nil, fmt.Errorf("failed to create benchmark quotas, err=%v", err)
	}
	env := &benchEnv{plugin: plugin, gqm: gqm, quotas: quotas}
	envCache[envKey] = env
	return env, nil
}

func addQuotaCustomLimitFn(b *testing.B, env *benchEnv, resourceValue, numCustomLimiters, targetLevel int) {
	targetQuotaName := getTargetQuotaName(targetLevel)
	targetQuota := env.quotas[targetQuotaName].DeepCopy()
	for i := 0; i < numCustomLimiters; i++ {
		testCustomKey := fmt.Sprintf("mock%d", i)
		testAnnoKey := fmt.Sprintf(core.AnnotationKeyMockLimitFmt, testCustomKey)
		targetQuota.Annotations[testAnnoKey] = fmt.Sprintf(`{"cpu": %d, "memory": %d}`,
			resourceValue, resourceValue)
	}
	b.StartTimer()
	err := env.gqm.UpdateQuota(targetQuota, false)
	b.StopTimer()
	if err != nil {
		b.Fatalf("failed to add custom-limit to quota %s, err: %v", targetQuota.Name, err)
	}
}

func deleteQuotaCustomLimitFn(b *testing.B, env *benchEnv, numCustomLimiters, targetLevel int) {
	targetQuotaName := getTargetQuotaName(targetLevel)
	targetQuota := env.quotas[targetQuotaName].DeepCopy()
	// rollback
	for i := 0; i < numCustomLimiters; i++ {
		testCustomKey := fmt.Sprintf("mock%d", i)
		testAnnoKey := fmt.Sprintf(core.AnnotationKeyMockLimitFmt, testCustomKey)
		delete(targetQuota.Annotations, testAnnoKey)
	}
	err := env.gqm.UpdateQuota(targetQuota, false)
	if err != nil {
		b.Fatalf("failed to delete custom-limit for quota %s, err: %v", targetQuota.Name, err)
	}
}

var pod = schetesting.MakePod().Name("").Containers([]corev1.Container{schetesting.MakeContainer().Name("0").Resources(
	map[corev1.ResourceName]string{corev1.ResourceCPU: strconv.Itoa(1),
		corev1.ResourceMemory: strconv.Itoa(1)}).Obj()}).Node("node0").Obj()

func addPodFn(b *testing.B, env *benchEnv, runIndex, numCustomLimiters, targetLevel int) {
	targetQuotaName := getTargetQuotaName(targetLevel)
	podName := fmt.Sprintf("pod_%d", runIndex)
	pod.SetName(podName)
	pod.Labels = map[string]string{
		extension.LabelQuotaName: targetQuotaName,
	}
	for i := 0; i < numCustomLimiters; i++ {
		testCustomKey := fmt.Sprintf("mock%d", i)
		pod.Labels[testCustomKey] = "true"
	}
	b.StartTimer()
	env.gqm.OnPodAdd(targetQuotaName, pod)
	b.StopTimer()
}
func checkCustomUsedFn(b *testing.B, env *benchEnv, n int64) {
	// check custom-used
	targetQuotaName := getTargetQuotaName(3)
	quotaInfo := env.gqm.GetQuotaInfoByName(targetQuotaName)
	for i := 0; i < 3; i++ {
		customKey := fmt.Sprintf("mock%d", i)
		customUsed := quotaInfo.GetCustomUsedByKey(customKey)
		if customUsed == nil {
			b.Fatalf("custom-used for %s is nil", customKey)
		}
		if customUsed.Cpu().Value() != n || customUsed.Memory().Value() != n {
			b.Fatalf("custom-used for %s is not expected, n=%v, got %v", customKey, n, customUsed)
		}
		b.Logf("custom-used for %s is %v, n=%v", customKey, printResourceList(customUsed), n)
	}
}

func checkUsedFn(b *testing.B, env *benchEnv, targetLevel int, n int64) {
	// check custom-used
	targetQuotaName := getTargetQuotaName(targetLevel)
	quotaInfo := env.gqm.GetQuotaInfoByName(targetQuotaName)
	used := quotaInfo.GetUsed()
	if used.Cpu().Value() != n || used.Memory().Value() != n {
		b.Fatalf("used is not expected, n=%v, got cpu=%v, memory=%v", n,
			used.Cpu().Value(), used.Memory().Value())
	}
}

func checkPodFn(b *testing.B, env *benchEnv, runIndex, numCustomLimiters, targetLevel int,
	checkStatusFn func(b *testing.B, status *framework.Status)) {
	targetQuotaName := getTargetQuotaName(targetLevel)
	podName := fmt.Sprintf("pod_%d", runIndex)
	pod.SetName(podName)
	pod.Labels = map[string]string{
		extension.LabelQuotaName: targetQuotaName,
	}
	for i := 0; i < numCustomLimiters; i++ {
		testCustomKey := fmt.Sprintf("mock%d", i)
		pod.Labels[testCustomKey] = "true"
	}
	state := framework.NewCycleState()
	b.StartTimer()
	_, status := env.plugin.PreFilter(context.TODO(), state, pod)
	b.StopTimer()
	if checkStatusFn != nil {
		checkStatusFn(b, status)
	}
}

func createBenchmarkQuotasRecursive(plugin *Plugin, parentName string,
	curLevel, numPodsPerQuota int, numsPerLevel []int, quotas map[string]*v1alpha1.ElasticQuota) (err error) {
	if curLevel >= len(numsPerLevel) {
		return
	}
	curNum := numsPerLevel[curLevel]
	isParent := curLevel != len(numsPerLevel)-1
	for no := 0; no < curNum; no++ {
		q := CreateQuota2(fmt.Sprintf("%s_%d", parentName, no), parentName, 100, 100, 0, 0, 0, 0, isParent, "")
		plugin.OnQuotaAdd(q)
		quotas[q.Name] = q
		if !isParent && numPodsPerQuota > 0 {
			for i := 0; i < numPodsPerQuota; i++ {
				newPod := pod.DeepCopy()
				newPod.SetName(fmt.Sprintf("pod%s_%d", q.Name, i))
				newPod.Labels = map[string]string{
					extension.LabelQuotaName: q.Name,
					"mock0":                  "true",
				}
				plugin.OnPodAdd(newPod)
			}
		}
		err = createBenchmarkQuotasRecursive(plugin, q.Name, curLevel+1, numPodsPerQuota, numsPerLevel, quotas)
		if err != nil {
			return
		}
	}
	return
}
