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

package prediction

import (
	"flag"
	"time"
)

type Config struct {
	CheckpointFilepath           string
	ColdStartDuration            time.Duration
	SafetyMarginPercent          int
	MemoryHistogramDecayHalfLife time.Duration
	CPUHistogramDecayHalfLife    time.Duration
	TrainingInterval             time.Duration
	ModelExpirationDuration      time.Duration
	ModelCheckpointInterval      time.Duration
	ModelCheckpointMaxPerStep    int
}

func NewDefaultConfig() *Config {
	return &Config{
		CheckpointFilepath:           "/prediction-checkpoints",
		ColdStartDuration:            24 * time.Hour,
		SafetyMarginPercent:          10,
		MemoryHistogramDecayHalfLife: 24 * time.Hour,
		CPUHistogramDecayHalfLife:    12 * time.Hour,
		TrainingInterval:             time.Minute,
		ModelExpirationDuration:      30 * time.Minute,
		ModelCheckpointInterval:      10 * time.Minute,
		ModelCheckpointMaxPerStep:    12,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.CheckpointFilepath, "prediction-checkpoint-filepath", c.CheckpointFilepath, "The filepath is used to store the checkpoints of the prediction system's state.")
	fs.DurationVar(&c.ColdStartDuration, "prediction-cold-start-duration", c.ColdStartDuration, "Cold start refers to the period of time after a Pod starts and enters into a stable state")
	fs.IntVar(&c.SafetyMarginPercent, "prediction-safety-margin-percent", c.SafetyMarginPercent, "The redundancy preserved above peak prediction")
	fs.DurationVar(&c.MemoryHistogramDecayHalfLife, "prediction-memory-histogram-decay-halflife", c.MemoryHistogramDecayHalfLife, "Half-life, the older the data, the lower the weight")
	fs.DurationVar(&c.CPUHistogramDecayHalfLife, "prediction-cpu-histogram-decay-halflife", c.CPUHistogramDecayHalfLife, "Half-life, the older the data, the lower the weight")
	fs.DurationVar(&c.TrainingInterval, "prediction-training-interval", c.TrainingInterval, "Period of prediction model update")
	fs.DurationVar(&c.ModelExpirationDuration, "prediction-model-expiration-duration", c.ModelExpirationDuration, "Expiration of prediction model without updated")
	fs.DurationVar(&c.ModelCheckpointInterval, "prediction-model-checkpoint-interval", c.ModelCheckpointInterval, "Interval of prediction model take checkpoint")
	fs.IntVar(&c.ModelCheckpointMaxPerStep, "prediction-model-checkpoint-max-per-step", c.ModelCheckpointMaxPerStep, "The maximum number of prediction models saved at a time")
}
