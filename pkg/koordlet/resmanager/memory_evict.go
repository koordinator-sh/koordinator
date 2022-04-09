/*
 * Copyright 2022 The Koordinator Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package resmanager

import (
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
)

const (
	memoryReleaseBufferPercent = 2
)

type MemoryEvictor struct {
	resManager    *resManager
	lastEvictTime time.Time
}

type podInfo struct {
	pod       *corev1.Pod
	podMetric *metriccache.PodResourceMetric
}

func NewMemoryEvictor(mgr *resManager) *MemoryEvictor {
	return &MemoryEvictor{
		resManager:    mgr,
		lastEvictTime: time.Now(),
	}
}
