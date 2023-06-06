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

import (
	"encoding/xml"
	"io/ioutil"
	"log"
	"os"
	"strconv"
)

var (
	CORE_DEFAULT Resource = Resource{"core-default.xml", false}
	CORE_SITE    Resource = Resource{"core-site.xml", true}
	HDFS_DEFAULT Resource = Resource{"hdfs-default.xml", false}
	HDFS_SITE    Resource = Resource{"hdfs-site.xml", true}
)

type Resource struct {
	Name     string
	Required bool
}

type Configuration interface {
	Get(key string, defaultValue string) (string, error)
	GetInt(key string, defaultValue int) (int, error)

	Set(key string, value string) error
	SetInt(key string, value int) error
}

type configuration struct {
	Properties map[string]string
}

type property struct {
	Name  string `xml:"name"`
	Value string `xml:"value"`
}

type hadoopConfiguration struct {
	XMLName    xml.Name   `xml:"configuration"`
	Properties []property `xml:"property"`
}

func (conf *configuration) Get(key string, defaultValue string) (string, error) {
	value, exists := conf.Properties[key]
	if !exists {
		return defaultValue, nil
	}
	return value, nil
}

func (conf *configuration) GetInt(key string, defaultValue int) (int, error) {
	value, exists := conf.Properties[key]
	if !exists {
		return defaultValue, nil
	}
	return strconv.Atoi(value)
}

func (conf *configuration) Set(key string, value string) error {
	conf.Properties[key] = value
	return nil
}

func (conf *configuration) SetInt(key string, value int) error {
	conf.Properties[key] = strconv.Itoa(value)
	return nil
}

func NewConfiguration(hadoopConfDir string) (Configuration, error) {
	return NewConfigurationResources(hadoopConfDir, []Resource{})
}

func NewConfigurationResources(hadoopConfDir string, resources []Resource) (Configuration, error) {
	// Add $HADOOP_CONF_DIR/core-default.xml & $HADOOP_CONF_DIR/core-site.xml
	resourcesWithDefault := []Resource{CORE_DEFAULT, CORE_SITE}
	resourcesWithDefault = append(resourcesWithDefault, resources...)

	c := configuration{Properties: make(map[string]string)}

	for _, resource := range resourcesWithDefault {
		conf, err := os.Open(hadoopConfDir + string(os.PathSeparator) + resource.Name)
		if err != nil {
			if !resource.Required {
				continue
			}
			log.Fatal("Couldn't open resource: ", err)
			return nil, err
		}
		confData, err := ioutil.ReadAll(conf)
		if err != nil {
			log.Fatal("Couldn't read resource: ", err)
			return nil, err
		}
		defer conf.Close()

		// Parse
		var hConf hadoopConfiguration
		err = xml.Unmarshal(confData, &hConf)
		if err != nil {
			log.Fatal("Couldn't parse core-site.xml: ", err)
			return nil, err
		}

		// Save into configuration
		for _, kv := range hConf.Properties {
			c.Set(kv.Name, kv.Value)
		}
	}

	return &c, nil
}
