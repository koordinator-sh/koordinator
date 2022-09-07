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

package reporter

import (
	"flag"
	"time"
)

type Config struct {
	ReportInterval time.Duration
}

func NewDefaultConfig() *Config {
	return &Config{
		ReportInterval: 60 * time.Second,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.DurationVar(&c.ReportInterval, "report-interval", c.ReportInterval, "Report interval time. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
}
