/*
 Copyright 2024 The Koordinator Authors.

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

package model

import (
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/checkpoint"
	"time"
)

type ModelKey interface {
	Key() string
}

type Model interface {
	ModelKey
}

type Sample interface {
	Value() (time.Time, float64)
}

type DistributionModel interface {
	Model
	AddSample(sample Sample)
	SubtractSample(sample Sample)
	LoadSnapshot(snapshot checkpoint.Snapshot) error
	SaveSnapshot(snapshot checkpoint.Snapshot) error
}
