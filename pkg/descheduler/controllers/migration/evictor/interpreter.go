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

package evictor

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/flowcontrol"

	sev1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (
	LabelEvictPolicy string = "koordinator.sh/evict-policy"
)

var (
	ErrTooManyEvictions = errors.New("TooManyEvictions")
)

type FactoryFn func(client kubernetes.Interface) Interface

type Interface interface {
	Evict(ctx context.Context, job *sev1alpha1.PodMigrationJob, pod *corev1.Pod) error
}

var registry = map[string]FactoryFn{}

func RegisterEvictor(name string, factoryFn FactoryFn) {
	registry[name] = factoryFn
}

type Interpreter interface {
	Interface
}

type interpreterImpl struct {
	evictions       map[string]Interface
	defaultEviction Interface
	rateLimiter     flowcontrol.RateLimiter
}

func NewInterpreter(client kubernetes.Interface, defaultEvictionPolicy string, evictQPS string, evictBurst int) (Interpreter, error) {
	var qps float32
	if val, err := strconv.ParseFloat(evictQPS, 64); err == nil && val > 0 {
		qps = float32(val)
	}
	rateLimiter := flowcontrol.NewTokenBucketRateLimiter(qps, evictBurst)

	evictions := map[string]Interface{}
	for k, v := range registry {
		evictions[k] = v(client)
	}
	defaultEviction := evictions[defaultEvictionPolicy]
	if defaultEviction == nil {
		return nil, fmt.Errorf("unsupport Evicition policy")
	}
	return &interpreterImpl{
		evictions:       evictions,
		defaultEviction: defaultEviction,
		rateLimiter:     rateLimiter,
	}, nil
}

func (p *interpreterImpl) Evict(ctx context.Context, job *sev1alpha1.PodMigrationJob, pod *corev1.Pod) error {
	if p.rateLimiter != nil {
		if !p.rateLimiter.TryAccept() {
			return ErrTooManyEvictions
		}
	}
	action := getCustomEvictionPolicy(pod.Labels)
	if action == "" {
		action = getCustomEvictionPolicy(job.Labels)
	}
	if action != "" {
		evictor := p.evictions[action]
		if evictor != nil {
			return evictor.Evict(ctx, job, pod)
		}
	}
	return p.defaultEviction.Evict(ctx, job, pod)
}

func getCustomEvictionPolicy(labels map[string]string) string {
	value, ok := labels[LabelEvictPolicy]
	if ok && value != "" {
		return value
	}
	return ""
}
