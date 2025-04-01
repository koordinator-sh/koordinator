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
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierror "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	"golang.org/x/exp/maps"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

const (
	CustomLimiterKeyMaxLen = 16
)

// CustomLimiter provides a mechanism to extend custom limiters, allows users to define custom logic
// for limiting resource usage within quotas, providing greater flexibility and control over resource management.
// For example, we can have a custom limiter to limit the resource usage of specified pods.
type CustomLimiter interface {
	// GetKey returns the unique identifier for the custom limiter.
	GetKey() string

	// GetLimitConf parses the custom limit configuration for a given quota.
	// Returns nil if no limit is configured for this quota, or non-nil CustomLimitConf if custom limiter is configured.
	GetLimitConf(quota *v1alpha1.ElasticQuota) (*CustomLimitConf, error)

	// CalculatePodUsedDelta calculates the delta of used resource for a pod update.
	// Returns the delta of used resource, adhering to the following rules:
	// - nil if no limit is configured for this quota
	// - non-nil resource if current custom limiter is configured for this quota
	// Called with the quotaInfo lock held, please ensure to access quotaInfo without acquiring the lock again.
	CalculatePodUsedDelta(quotaInfo *QuotaInfo, newPod, oldPod *corev1.Pod,
		quotaChainSharedState *CustomLimiterState) (usedDelta corev1.ResourceList)

	// Check checks custom limit for a quota by providing pod-request resource and the shared state of this quota
	// chain, the check process should start from the leaf quota and proceed to parents.
	// Called without holding the quotaInfo lock, ensure to acquire the lock when accessing quotaInfo.
	Check(quotaInfo *QuotaInfo, podRequest corev1.ResourceList, quotaChainSharedState *CustomLimiterState) error
}

type CustomArgs interface {
	DeepCopy() CustomArgs
	DeepEquals(args CustomArgs) bool
}

type CustomLimiterFactory func(key, args string) (CustomLimiter, error)

// customLimiterFactories is a global map of registered custom limiter factories, keyed by factory key.
var customLimiterFactories = map[string]CustomLimiterFactory{}

func RegisterCustomLimiterFactory(factoryKey string, customLimiterFactory CustomLimiterFactory) {
	customLimiterFactories[factoryKey] = customLimiterFactory
}

func GetCustomLimiterFactory(factoryKey string) (CustomLimiterFactory, error) {
	limiterFactory := customLimiterFactories[factoryKey]
	if limiterFactory == nil {
		return nil, fmt.Errorf("custom limiter factory %s not found", factoryKey)
	}
	return limiterFactory, nil
}

// CustomLimiterState provides a mechanism for custom-limiters to store and retrieve arbitrary data
// in the update or check process of a quota chain in a bottom-up traversal (from leaf to root).
// Storage is keyed with StateKey, and valued with StateData.
// StateData can be shared among different custom-limiters, also can be shared among different quotas
// belongs to the same quota chain in a bottom-up traversal.
type CustomLimiterState struct {
	Storage sync.Map
	pod     *corev1.Pod
}

func NewCustomLimiterState() *CustomLimiterState {
	return &CustomLimiterState{}
}

func (cls *CustomLimiterState) WithPod(pod *corev1.Pod) *CustomLimiterState {
	cls.pod = pod
	return cls
}

func (cls *CustomLimiterState) GetPod() *corev1.Pod {
	return cls.pod
}

func (cls *CustomLimiterState) WithResourceList(stateKey string, value corev1.ResourceList) *CustomLimiterState {
	cls.Storage.Store(stateKey, value)
	return cls
}

func (cls *CustomLimiterState) GetResourceList(stateKey string) corev1.ResourceList {
	if v, ok := cls.Storage.Load(stateKey); ok {
		return v.(corev1.ResourceList)
	}
	return corev1.ResourceList{}
}

func GetCustomLimiters(pluginArgs *config.ElasticQuotaArgs) (rst map[string]CustomLimiter, err error) {
	rst = make(map[string]CustomLimiter)
	var errs []error
	for key, conf := range pluginArgs.CustomLimiters {
		if len(key) == 0 {
			errs = append(errs, fmt.Errorf("failed to initialize custom limiter with empty key"))
			continue
		}
		if len(key) > CustomLimiterKeyMaxLen {
			errs = append(errs, fmt.Errorf("failed to initialize custom limiter %s: %s", key,
				validation.MaxLenError(CustomLimiterKeyMaxLen)))
			continue
		}
		if inErrs := validation.IsDNS1123Label(key); len(inErrs) > 0 {
			errs = append(errs, fmt.Errorf("failed to initialize custom limiter %s: %+v", key, inErrs))
			continue
		}
		if conf.FactoryKey == "" {
			errs = append(errs, fmt.Errorf("failed to initialize custom limiter %s: factory key is empty", key))
			continue
		}
		factory, err := GetCustomLimiterFactory(conf.FactoryKey)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to initialize custom limiter %s: factory %s not found",
				key, conf.FactoryKey))
			continue
		}
		limiter, err := factory(key, conf.FactoryArgs)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to initialize custom limiter %s by factory %s, err=%v",
				key, conf.FactoryKey, err))
			continue
		}
		rst[key] = limiter
	}
	if len(errs) > 0 {
		return nil, apierror.NewAggregate(errs)
	}
	klog.Infof("initialized custom limiters: %+v", maps.Keys(rst))
	return rst, nil
}

func customArgsDeepEqual(a, b CustomArgs) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.DeepEquals(b)
}

type CustomLimitConf struct {
	Limit corev1.ResourceList
	Args  CustomArgs
}

func (clc *CustomLimitConf) DeepCopy() *CustomLimitConf {
	if clc == nil {
		return nil
	}
	var args CustomArgs
	if clc.Args != nil {
		args = clc.Args.DeepCopy()
	}
	return &CustomLimitConf{
		Limit: clc.Limit.DeepCopy(),
		Args:  args,
	}
}

func customLimitConfEquals(a, b *CustomLimitConf) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return quotav1.Equals(a.Limit, b.Limit) && customArgsDeepEqual(a.Args, b.Args)
}

type CustomLimitConfMap map[string]*CustomLimitConf

func (clcm CustomLimitConfMap) DeepCopy() CustomLimitConfMap {
	if clcm == nil {
		return nil
	}
	target := make(CustomLimitConfMap)
	for k, v := range clcm {
		target[k] = v.DeepCopy()
	}
	return target
}

func customLimitConfMapEquals(a, b CustomLimitConfMap) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if !customLimitConfEquals(v, bv) {
			return false
		}
	}
	return true
}

type CustomResourceLists map[string]corev1.ResourceList

func (crl CustomResourceLists) DeepCopy() CustomResourceLists {
	if crl == nil {
		return nil
	}
	target := make(CustomResourceLists)
	for k, v := range crl {
		target[k] = v.DeepCopy()
	}
	return target
}
