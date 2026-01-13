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

package frameworkext

import (
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

type ReservationCache interface {
	DeleteReservation(r *schedulingv1alpha1.Reservation) *ReservationInfo
	GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *ReservationInfo
}

// TODO(joseph): Considering the amount of changed code,
// temporarily use global variable to store ReservationCache instance,
// and then refactor to separate ReservationCache later.
var reservationCacheMap = &sync.Map{}

func GetAllReservationCaches() map[string]ReservationCache {
	m := map[string]ReservationCache{}
	reservationCacheMap.Range(func(key, value interface{}) bool {
		m[key.(string)] = value.(ReservationCache)
		return true
	})
	if len(m) <= 0 {
		return nil
	}
	return m
}

func SetReservationCache(cache ReservationCache, profileName string) {
	reservationCacheMap.Store(profileName, cache)
	klog.V(5).Infof("SetReservationCache, profileName: %s", profileName)
}

func ClearReservationCache() {
	reservationCacheMap = &sync.Map{}
	klog.V(5).Infof("ClearReservationCache")
}

var _ ReservationCache = &FakeReservationCache{}

type FakeReservationCache struct {
	RInfo *ReservationInfo
}

func NewFakeReservationCache() *FakeReservationCache {
	return &FakeReservationCache{}
}

func (f *FakeReservationCache) DeleteReservation(r *schedulingv1alpha1.Reservation) *ReservationInfo {
	if f.RInfo != nil {
		return f.RInfo
	}
	return NewReservationInfo(r)
}

func (f *FakeReservationCache) GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *ReservationInfo {
	return f.RInfo
}

func (f *FakeReservationCache) GetReservationInfo(uid types.UID) *ReservationInfo {
	return f.RInfo
}
