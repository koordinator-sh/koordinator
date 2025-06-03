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

package operator

import (
	"errors"
	"fmt"
	"math"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/cgroup"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/interpolate"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/podcgroup"
)

const AnnotaionMemorySuppress = "koordinator.sh/memory-suppress"

var DefaultMemorySuppress Operator = &MemorySuppress{
	MinSpot:     0.5,
	MaxSpot:     0.9,
	GrowPeriods: 10,
	KillPeriods: 60,
}

type MemorySuppress struct {
	MinSpot     float64 `json:"minSpot,omitempty"`
	MaxSpot     float64 `json:"maxSpot,omitempty"`
	GrowPeriods int64   `json:"growPeriods,omitempty"`
	KillPeriods int64   `json:"killPeriods,omitempty"`

	primaryCache   map[int]interpolate.Primary `json:"-"`
	maxSpotCounter map[types.UID]float64       `json:"-"`
}

func (ms *MemorySuppress) Init() error { return nil }

func (ms *MemorySuppress) Name() string {
	return "MemorySuppress"
}

func (ms *MemorySuppress) Update(g Operator) error {
	if v, ok := g.(*MemorySuppress); ok {
		ms.MinSpot = v.MinSpot
		ms.MaxSpot = v.MaxSpot
		ms.GrowPeriods = v.GrowPeriods
		ms.KillPeriods = v.KillPeriods
		return nil
	}
	return fmt.Errorf("not a MemorySuppress")
}

func (ms *MemorySuppress) Exec(pods map[types.UID]*podcgroup.PodCgroup, node *v1.Node) (retErr error) {
	var show []string
	for _, pc := range pods {
		if pc.Pod.Annotations[AnnotaionMemorySuppress] != "true" {
			continue
		}
		mem := podcgroup.Memory.Get(pc)
		target := ms.targetThrottle(mem)
		if target == mem.Throttle().Int64() {
			continue
		}
		if err := mem.SetThrottle(target); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to set memory throttle for pod %s: %w", klog.KObj(pc.Pod), err))
			continue
		}
		show = append(show, fmt.Sprintf("%s: %v/%v", klog.KObj(pc.Pod), mem.Format(target), mem.Format(mem.Limit)))
	}
	if len(show) > 0 {
		klog.InfoS(ms.Name(), "info", show)
	}
	return retErr
}

func (ms *MemorySuppress) AddPod(pc *podcgroup.PodCgroup) error {
	if pc.Pod.Annotations[AnnotaionMemorySuppress] != "true" {
		return nil
	}
	if ms.maxSpotCounter == nil {
		ms.maxSpotCounter = make(map[types.UID]float64)
	}
	ms.maxSpotCounter[pc.Pod.UID] = 0
	return nil
}

func (ms *MemorySuppress) DeletePod(pc *podcgroup.PodCgroup) error {
	delete(ms.maxSpotCounter, pc.Pod.UID)
	return nil
}

func (ms *MemorySuppress) targetThrottle(r *podcgroup.PodResource) int64 {
	if r.Limit <= r.Request {
		if r.Limit == 0 {
			return cgroup.Max
		}
		return r.Limit
	}

	burst := float64(r.Limit - r.Request)
	current := float64(r.Current().Int64() - r.Request)
	spot := math.Max(current/burst, ms.MinSpot)

	if spot >= ms.MaxSpot { // accumulation of pressure in max spot for killing pod
		ms.maxSpotCounter[r.Pod.UID] += r.Pressure().Rate()
		if ms.maxSpotCounter[r.Pod.UID] > 0.5*float64(ms.KillPeriods) {
			klog.InfoS("Pod in max pressure spot", "pod", klog.KObj(r.Pod), "spot", spot,
				"accumulation", fmt.Sprintf("%.2f/%d", ms.maxSpotCounter[r.Pod.UID], ms.KillPeriods))
		}
		if ms.maxSpotCounter[r.Pod.UID] > float64(ms.KillPeriods) {
			klog.InfoS("Pod in max pressure spot excceed max periods, kill it", "pod", klog.KObj(r.Pod), "spot", spot,
				"accumulation", fmt.Sprintf("%.2f/%d", ms.maxSpotCounter[r.Pod.UID], ms.KillPeriods))
			return cgroup.Max
		}
	} else {
		ms.maxSpotCounter[r.Pod.UID] = 0
	}

	rate := ms.getTarget(spot, r.Pressure().Rate(), ms.getPrimary(2))
	return r.Request + int64(burst*rate)
}

func (ms *MemorySuppress) getTarget(spot, pressure float64, primary interpolate.Primary) float64 {
	step := primary.Interpolate(spot-ms.MinSpot) * (ms.MaxSpot - ms.MinSpot)
	return math.Min(spot+step*pressure, ms.MaxSpot)
}

func (ms *MemorySuppress) getPrimary(power int) interpolate.Primary {
	if primary, ok := ms.primaryCache[power]; ok {
		return primary
	}
	var primary interpolate.Primary
	sum := 0.
	for i := 0; i < int(ms.GrowPeriods); i++ {
		p := interpolate.Point{Y: math.Pow(1-float64(i)/float64(ms.GrowPeriods), float64(power))}
		sum += p.Y
		primary = append(primary, p)
	}
	for i := range primary {
		primary[i].Y /= sum
		if i > 0 {
			primary[i].X = primary[i-1].X + primary[i-1].Y
		}
		if i == len(primary)-1 {
			primary[i].Y = 1 - primary[i].X
		}
	}
	if len(primary) == 0 {
		primary.Add(interpolate.Point{X: 1, Y: 1})
	}
	if ms.primaryCache == nil {
		ms.primaryCache = make(map[int]interpolate.Primary)
	}
	ms.primaryCache[power] = primary
	return primary
}
