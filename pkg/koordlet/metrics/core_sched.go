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

package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/koordinator-sh/koordinator/pkg/util/metrics"
)

const (
	CoreSchedCookieKey = "core_sched_cookie"
	CoreSchedGroupKey  = "core_sched_group"
)

var (
	ContainerCoreSchedCookie = metrics.NewGCGaugeVec("container_core_sched_cookie", prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "container_core_sched_cookie",
		Help:      "the core scheduling cookie of the container",
	}, []string{NodeKey, PodName, KoordPodName, PodNamespace, PodUID, ContainerName, ContainerID, CoreSchedGroupKey, CoreSchedCookieKey}))

	CoreSchedCookieManageStatus = metrics.NewGCCounterVec("core_sched_cookie_manage_status", prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: KoordletSubsystem,
		Name:      "core_sched_cookie_manage_status",
		Help:      "the manage status of the core scheduling cookie",
	}, []string{NodeKey, CoreSchedGroupKey, StatusKey}))

	CoreSchedCollector = []prometheus.Collector{
		ContainerCoreSchedCookie.GetGaugeVec(),
		CoreSchedCookieManageStatus.GetCounterVec(),
	}
)

func RecordContainerCoreSchedCookie(namespace, podName, podUID, containerName, containerID, groupID string, cookieID uint64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[PodNamespace] = namespace
	labels[PodName] = podName
	labels[KoordPodName] = podName
	labels[PodUID] = podUID
	labels[ContainerName] = containerName
	labels[ContainerID] = containerID
	labels[CoreSchedGroupKey] = groupID
	labels[CoreSchedCookieKey] = strconv.FormatUint(cookieID, 10)
	ContainerCoreSchedCookie.WithSet(labels, 1.0)
}

func ResetContainerCoreSchedCookie(namespace, podName, podUID, containerName, containerID, groupID string, cookieID uint64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[PodNamespace] = namespace
	labels[PodName] = podName
	labels[KoordPodName] = podName
	labels[PodUID] = podUID
	labels[ContainerName] = containerName
	labels[ContainerID] = containerID
	labels[CoreSchedGroupKey] = groupID
	labels[CoreSchedCookieKey] = strconv.FormatUint(cookieID, 10)
	ContainerCoreSchedCookie.Delete(labels)
}

func RecordCoreSchedCookieManageStatus(groupID string, isSucceeded bool) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[CoreSchedGroupKey] = groupID
	labels[StatusKey] = StatusSucceed
	if !isSucceeded {
		labels[StatusKey] = StatusFailed
	}
	CoreSchedCookieManageStatus.WithInc(labels)
}
