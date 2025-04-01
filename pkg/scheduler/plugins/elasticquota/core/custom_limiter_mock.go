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

package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	CustomKeyMock             = "mock"
	AnnotationKeyMockLimitFmt = "custom-%s-limit-conf"
	AnnotationKeyMockArgsFmt  = "custom-%s-args"

	MockLimiterFactoryKey = "mock-limiter-factory"
)

type MockLimiterFactoryArgs struct {
	LabelSelector string `json:"labelSelector"`
}

func NewMockLimiter(key, args string) (limiter CustomLimiter, err error) {
	if args == "" {
		return nil, fmt.Errorf("args is required but not found")
	}
	var factoryArgs MockLimiterFactoryArgs
	if err := json.Unmarshal([]byte(args), &factoryArgs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal args for custom limiter factory %s, err=%v", CustomKeyMock, err)
	}
	if factoryArgs.LabelSelector == "" {
		err = fmt.Errorf("labelSelector is required but not found")
		return
	}
	var tmpSelector *metav1.LabelSelector
	tmpSelector, err = metav1.ParseToLabelSelector(factoryArgs.LabelSelector)
	if err != nil {
		err = fmt.Errorf("failed to parse labelSelector, err=%v", err)
		return
	}
	selector, err := metav1.LabelSelectorAsSelector(tmpSelector)
	if err != nil {
		err = fmt.Errorf("failed to convert labels.Selector, err=%v", err)
		return
	}
	return &MockCustomLimiter{
		key:      key,
		selector: selector,
	}, nil
}

type MockCustomLimiter struct {
	key      string
	selector labels.Selector
}

func (m *MockCustomLimiter) GetKey() string {
	return m.key
}

func (m *MockCustomLimiter) GetLimitConf(quota *v1alpha1.ElasticQuota) (*CustomLimitConf, error) {
	annoKeyLimitConf := fmt.Sprintf(AnnotationKeyMockLimitFmt, m.GetKey())
	var limit corev1.ResourceList
	if mockLimitConfV := quota.Annotations[annoKeyLimitConf]; mockLimitConfV != "" {
		if err := json.Unmarshal([]byte(mockLimitConfV), &limit); err != nil {
			return nil, fmt.Errorf("failed to unmarshal custom limit for quota %s, key=%s, err=%v",
				quota.Name, m.GetKey(), err)
		}
	}
	if limit == nil {
		return nil, nil
	}
	var args CustomArgs
	annoKeyArgs := fmt.Sprintf(AnnotationKeyMockArgsFmt, m.GetKey())
	if mockArgsConfV := quota.Annotations[annoKeyArgs]; mockArgsConfV != "" {
		var mockArgs MockCustomArgs
		if err := json.Unmarshal([]byte(mockArgsConfV), &mockArgs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal custom args for quota %s, key=%s, err=%v",
				quota.Name, m.GetKey(), err)
		}
		args = &mockArgs
	}
	return &CustomLimitConf{Limit: limit, Args: args}, nil
}

func (m *MockCustomLimiter) CalculatePodUsedDelta(quotaInfo *QuotaInfo, newPod, oldPod *corev1.Pod,
	_ *CustomLimiterState) (
	usedDelta corev1.ResourceList) {
	if newPod == nil && oldPod == nil {
		return
	}
	key := m.GetKey()
	// skip calculating used if no configured limit
	limit := quotaInfo.GetCustomLimitNoLock(key)
	if limit == nil {
		return nil
	}
	args := quotaInfo.GetCustomArgsNoLock(key)
	// debug log
	if m.isDebugEnabled(args) || klog.V(5).Enabled() {
		podKey := GetPodKey(newPod, oldPod)
		defer func() {
			klog.Infof("calculated custom-used-delta for pod %s, quota=%s, key=%s, usedDelta=%v",
				podKey, quotaInfo.Name, key, util.PrintResourceList(usedDelta))
		}()
	}
	// get request resource for matched pod
	var oldReq, newReq corev1.ResourceList
	if oldPod != nil {
		oldReq = m.getPodRequest(oldPod)
	}
	if newPod != nil {
		newReq = m.getPodRequest(newPod)
	}
	usedDelta = quotav1.Mask(quotav1.Subtract(newReq, oldReq), quotav1.ResourceNames(limit))
	return
}

func (m *MockCustomLimiter) getPodRequest(pod *corev1.Pod) (req corev1.ResourceList) {
	if pod == nil {
		return
	}
	// filter out unmatched pod
	matched := m.selector.Matches(labels.Set(pod.Labels))
	if !matched {
		return
	}
	return util.GetPodRequest(pod)
}

func (m *MockCustomLimiter) isDebugEnabled(args CustomArgs) bool {
	return args != nil && args.(*MockCustomArgs).DebugEnabled
}

func (m *MockCustomLimiter) Check(quotaInfo *QuotaInfo, requestDelta corev1.ResourceList,
	state *CustomLimiterState) error {
	key := m.GetKey()
	// skip checking if no configured limit
	limit := quotaInfo.GetCustomLimit(key)
	if limit == nil {
		return nil
	}
	// verify pod is required
	pod := state.GetPod()
	if pod == nil {
		return fmt.Errorf("pod is required but not found")
	}
	debugEnabled := m.isDebugEnabled(quotaInfo.GetCustomArgs(key))

	// filter out unmatched pod
	matched := m.selector.Matches(labels.Set(pod.Labels))
	if debugEnabled || klog.V(5).Enabled() {
		klog.Infof("checked custom limit for pod %s, quota=%s, key=%s, matched=%v",
			util.GetPodKey(pod), m.GetKey(), quotaInfo.Name, matched)
	}
	if !matched {
		return nil
	}

	// check if the pod is allowed by the custom limiter
	curUsed := quotaInfo.CalculateInfo.CustomUsed[m.GetKey()]
	requestDelta = quotav1.Mask(requestDelta, quotav1.ResourceNames(limit))
	newUsed := quotav1.Add(curUsed, requestDelta)
	if notExceeded, exceededResourceNames := quotav1.LessThanOrEqual(newUsed, limit); !notExceeded {
		msg := fmt.Sprintf("insufficient resource, limit=%v, used=%v, request=%v, exceededResourceNames=%v",
			util.PrintResourceList(limit), util.PrintResourceList(curUsed),
			util.PrintResourceList(requestDelta), exceededResourceNames)
		if debugEnabled || klog.V(5).Enabled() {
			klog.Infof("check failed for quota %s by custom-limiter %s: %s", quotaInfo.Name, m.GetKey(), msg)
		}
		return errors.New(msg)
	}
	if debugEnabled || klog.V(5).Enabled() {
		klog.Infof("pod %s is allowed, quota=%s, customKey=%s, limit=%v, used=%v, request=%v",
			util.GetPodKey(pod), quotaInfo.Name, m.GetKey(), util.PrintResourceList(limit),
			util.PrintResourceList(curUsed), util.PrintResourceList(requestDelta))
	}

	return nil
}

type MockCustomArgs struct {
	DebugEnabled bool `json:"debugEnabled"`
}

func (mca *MockCustomArgs) DeepCopy() CustomArgs {
	return &MockCustomArgs{
		DebugEnabled: mca.DebugEnabled,
	}
}

func (mca *MockCustomArgs) DeepEquals(another CustomArgs) bool {
	if mca == nil && another == nil {
		return true
	}
	if mca == nil || another == nil {
		return false
	}
	return mca.DebugEnabled == another.(*MockCustomArgs).DebugEnabled
}

func GetPodKey(pods ...*corev1.Pod) string {
	for _, pod := range pods {
		if pod != nil {
			return util.GetPodKey(pod)
		}
	}
	return ""
}

func GetQuotaInfos(quotaInfoMap map[string]*QuotaInfo, names ...string) (quotaInfos []*QuotaInfo) {
	for _, name := range names {
		if q, ok := quotaInfoMap[name]; ok {
			quotaInfos = append(quotaInfos, q)
		}
	}
	return
}

type GetQuotaResListFn func(quotaInfo *QuotaInfo) corev1.ResourceList

func AssertCustomLimitEqual(t *testing.T, expected corev1.ResourceList, customKey string, quotaInfos ...*QuotaInfo) {
	assertCustomResListEqual(t, expected, customKey,
		func(quotaInfo *QuotaInfo, customKey string) corev1.ResourceList {
			return quotaInfo.GetCustomLimit(customKey)
		}, quotaInfos...)
}

func AssertCustomUsedEqual(t *testing.T, expected corev1.ResourceList, customKey string, quotaInfos ...*QuotaInfo) {
	assertCustomResListEqual(t, expected, customKey,
		func(quotaInfo *QuotaInfo, customKey string) corev1.ResourceList {
			return quotaInfo.GetCustomUsedByKey(customKey)
		}, quotaInfos...)
}

func assertCustomResListEqual(t *testing.T, expected corev1.ResourceList, customKey string,
	getResListFn func(quotaInfo *QuotaInfo, customKey string) corev1.ResourceList, quotaInfos ...*QuotaInfo) {
	for i, quotaInfo := range quotaInfos {
		actual := getResListFn(quotaInfo, customKey)
		if expected == nil && actual == nil {
			continue
		}
		if expected == nil {
			assert.Fail(t, fmt.Sprintf("index: %d, quota: %s\nexpected: nil\n  actual: %v",
				i, quotaInfo.Name, actual))
			continue
		}
		if actual == nil {
			assert.Fail(t, fmt.Sprintf("index: %d, quota: %s\nexpected: %v\n  actual: nil",
				i, quotaInfo.Name, expected))
			continue
		}
		if !quotav1.Equals(expected, actual) {
			assert.Fail(t, fmt.Sprintf("index: %d, quota: %s\nexpected: %v\n  actual: %v",
				i, quotaInfo.Name, expected, actual))
		}
	}
}

func AssertCustomLimiterState(t *testing.T, customKey string, expectedEnabled bool, quotaInfos ...*QuotaInfo) {
	for i, quotaInfo := range quotaInfos {
		if quotaInfo.IsEnabledCustomKeyNoLock(customKey) != expectedEnabled {
			assert.Fail(t, fmt.Sprintf("index: %d, quota: %s, customKey: %s, expectedEnabled: %v",
				i, quotaInfo.Name, customKey, expectedEnabled))
		}
	}
}
