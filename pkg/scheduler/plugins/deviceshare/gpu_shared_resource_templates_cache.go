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

package deviceshare

import (
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

type gpuSharedResourceTemplatesCache struct {
	lock sync.RWMutex
	// gpuSharedResourceTemplatesInfos stores GPUSharedResourceTemplates for each model of GPU which has it.
	gpuSharedResourceTemplatesInfos map[string]apiext.GPUSharedResourceTemplates
}

func newGPUSharedResourceTemplatesCache() *gpuSharedResourceTemplatesCache {
	// no need to make infos map because it would be directly initialized from configmap data
	return &gpuSharedResourceTemplatesCache{}
}

func (c *gpuSharedResourceTemplatesCache) getTemplates(vendor, model string) (apiext.GPUSharedResourceTemplates, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	key := fmt.Sprintf("%s-%s", vendor, model)
	if template, ok := c.gpuSharedResourceTemplatesInfos[key]; ok {
		return template, nil
	} else {
		return nil, fmt.Errorf("gpu shared resource template not found for %q", key)
	}
}

func (c *gpuSharedResourceTemplatesCache) setTemplatesInfos(infos map[string]apiext.GPUSharedResourceTemplates) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.gpuSharedResourceTemplatesInfos = infos
}

func (c *gpuSharedResourceTemplatesCache) setTemplatesInfosFromConfigMap(cm *corev1.ConfigMap) error {
	var infos map[string]apiext.GPUSharedResourceTemplates
	if err := yaml.Unmarshal([]byte(cm.Data["data.yaml"]), &infos); err != nil {
		return err
	}
	c.setTemplatesInfos(infos)
	return nil
}
