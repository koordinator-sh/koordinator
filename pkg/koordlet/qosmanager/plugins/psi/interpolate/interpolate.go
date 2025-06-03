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

package interpolate

import "sort"

type Point struct {
	X, Y float64
}

type Primary []Point

func NewPrimary(p ...Point) Primary {
	sort.Slice(p, func(i, j int) bool {
		return p[i].X < p[j].X
	})
	return p
}

func (l *Primary) Add(p Point) {
	for i := 0; i < len(*l); i++ {
		if (*l)[i].X == p.X {
			(*l)[i].Y = p.Y
			return
		}
		if (*l)[i].X > p.X {
			*l = append((*l)[:i], append([]Point{p}, (*l)[i:]...)...)
			return
		}
	}
}

func (l Primary) Interpolate(x float64) float64 {
	if len(l) == 0 {
		return 0
	}
	if x <= l[0].X {
		return l[0].Y
	}
	if x >= l[len(l)-1].X {
		return l[len(l)-1].Y
	}
	for i := 1; i < len(l); i++ {
		if x < l[i].X {
			return l[i-1].Y + (x-l[i-1].X)*(l[i].Y-l[i-1].Y)/(l[i].X-l[i-1].X)
		}
	}
	return 0
}
