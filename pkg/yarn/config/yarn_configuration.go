/*
Copyright 2013 The Cloudera Inc.
Copyright 2023 The Koordinator Authors.

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

package conf

var (
	YARN_DEFAULT Resource = Resource{"yarn-default.xml", false}
	YARN_SITE    Resource = Resource{"yarn-site.xml", true}
)

const (
	YARN_PREFIX                      = "yarn."
	RM_PREFIX                        = YARN_PREFIX + "resourcemanager."
	RM_ADDRESS                       = RM_PREFIX + "address"
	DEFAULT_RM_ADDRESS               = "0.0.0.0:8032"
	RM_SCHEDULER_ADDRESS             = RM_PREFIX + "scheduler.address"
	RM_ADMIN_ADDRESS                 = RM_PREFIX + "admin.address"
	DEFAULT_RM_SCHEDULER_ADDRESS     = "0.0.0.0:8030"
	DEFAULT_RM_ADMIN_ADDRESS         = "0.0.0.0:8033"
	RM_AM_EXPIRY_INTERVAL_MS         = YARN_PREFIX + "am.liveness-monitor.expiry-interval-ms"
	DEFAULT_RM_AM_EXPIRY_INTERVAL_MS = 600000
)

type yarn_configuration struct {
	conf Configuration
}

type YarnConfiguration interface {
	GetRMAddress() (string, error)
	GetRMSchedulerAddress() (string, error)
	GetRMAdminAddress() (string, error)

	SetRMAddress(address string) error
	SetRMSchedulerAddress(address string) error

	Get(key string, defaultValue string) (string, error)
	GetInt(key string, defaultValue int) (int, error)

	Set(key string, value string) error
	SetInt(key string, value int) error
}

func (yarn_conf *yarn_configuration) Get(key string, defaultValue string) (string, error) {
	return yarn_conf.conf.Get(key, defaultValue)
}

func (yarn_conf *yarn_configuration) GetInt(key string, defaultValue int) (int, error) {
	return yarn_conf.conf.GetInt(key, defaultValue)
}

func (yarn_conf *yarn_configuration) GetRMAddress() (string, error) {
	return yarn_conf.conf.Get(RM_ADDRESS, DEFAULT_RM_ADDRESS)
}

func (yarn_conf *yarn_configuration) GetRMSchedulerAddress() (string, error) {
	return yarn_conf.conf.Get(RM_SCHEDULER_ADDRESS, DEFAULT_RM_SCHEDULER_ADDRESS)
}

func (yarn_conf *yarn_configuration) GetRMAdminAddress() (string, error) {
	return yarn_conf.conf.Get(RM_ADMIN_ADDRESS, DEFAULT_RM_ADMIN_ADDRESS)
}

func (yarn_conf *yarn_configuration) Set(key string, value string) error {
	return yarn_conf.conf.Set(key, value)
}

func (yarn_conf *yarn_configuration) SetInt(key string, value int) error {
	return yarn_conf.conf.SetInt(key, value)
}

func (yarn_conf *yarn_configuration) SetRMAddress(address string) error {
	return yarn_conf.conf.Set(RM_ADDRESS, address)
}

func (yarn_conf *yarn_configuration) SetRMSchedulerAddress(address string) error {
	return yarn_conf.conf.Set(RM_SCHEDULER_ADDRESS, address)
}

func NewYarnConfiguration(hadooConfDir string) (YarnConfiguration, error) {
	c, err := NewConfigurationResources(hadooConfDir, []Resource{YARN_DEFAULT, YARN_SITE})
	return &yarn_configuration{conf: c}, err
}
