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
	PredictionCheckpointFilepath string
	PredictionColdStartDuration  time.Duration
}

func NewDefaultConfig() *Config {
	return &Config{
		PredictionCheckpointFilepath: "/prediction-checkpoints",
		PredictionColdStartDuration:  24 * time.Hour,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.PredictionCheckpointFilepath, "prediction-checkpoint-filepath", c.PredictionCheckpointFilepath, "The filepath is used to store the checkpoints of the prediction system's state.")
	fs.DurationVar(&c.PredictionColdStartDuration, "prediction-cold-start-duration", c.PredictionColdStartDuration, "Cold start refers to the period of time after a Pod starts and enters into a stable state")
}
