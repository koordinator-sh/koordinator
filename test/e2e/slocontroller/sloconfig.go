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

package slocontroller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
)

// sloConfigAction describes how to reach a wanted slo-controller-config value
// and how to put back what was there before.
//
// Kept separate from the API calls so the branch it decides can be tested
// without a cluster: the case this exists for -- a ConfigMap whose Data is nil
// -- is the one a kind environment does not reach, because the chart always
// creates the object with data.
type sloConfigAction struct {
	// needUpdate is false when the key already holds the wanted value.
	needUpdate bool
	// newData is what to write. Never nil when needUpdate is true.
	newData map[string]string
	// restoreData is what to write back on cleanup.
	restoreData map[string]string
	// removeKeys are keys the ConfigMap did not have, so cleanup drops them
	// rather than leaving a key the cluster never carried.
	removeKeys []string
	// restoreNil says the ConfigMap had no Data at all, so cleanup puts it
	// back to nil rather than writing an empty key over it.
	restoreNil bool
}

// planSLOConfigData decides how to set wanted on an existing ConfigMap.
//
// The nil case is the one that used to be handled and then crashed: the caller
// noticed Data == nil, recorded nothing to roll back, and wrote into the nil
// map anyway.
func planSLOConfigData(data map[string]string, wanted map[string]string) sloConfigAction {
	if data == nil {
		newData := make(map[string]string, len(wanted))
		for k, v := range wanted {
			newData[k] = v
		}
		return sloConfigAction{
			needUpdate: true,
			newData:    newData,
			restoreNil: true,
		}
	}

	needUpdate := false
	for k, v := range wanted {
		if current, ok := data[k]; !ok || current != v {
			needUpdate = true
			break
		}
	}
	if !needUpdate {
		// Already what we want; nothing to change and nothing to restore.
		return sloConfigAction{}
	}

	newData := make(map[string]string, len(data)+len(wanted))
	for k, v := range data {
		newData[k] = v
	}
	restoreData := map[string]string{}
	var removeKeys []string
	for k, v := range wanted {
		if current, ok := data[k]; ok {
			// The key existed with another value, so put that value back.
			restoreData[k] = current
		} else {
			// The key was not there at all. Restoring it to "" would leave a
			// key the cluster never had, so cleanup removes it instead.
			removeKeys = append(removeKeys, k)
		}
		newData[k] = v
	}

	return sloConfigAction{
		needUpdate:  true,
		newData:     newData,
		restoreData: restoreData,
		removeKeys:  removeKeys,
	}
}

// ensureSLOConfigData makes the slo-controller-config ConfigMap hold key=data
// and returns a cleanup that restores the state it found.
//
// It handles the three shapes a spec can meet, so a spec does not carry the
// boilerplate itself:
//
//   - the ConfigMap does not exist: it is created from the manifest, and
//     cleanup deletes it again;
//   - the ConfigMap exists with no Data: the map is initialised, and cleanup
//     puts Data back to nil;
//   - the ConfigMap exists with a different value: it is overwritten, and
//     cleanup writes the old value back (or drops the key if it was absent).
//
// The returned cleanup is always non-nil and is safe to defer even when
// nothing needed changing.
func ensureSLOConfigData(f *framework.Framework, namespace, name string, wanted map[string]string) func() {
	c := f.ClientSet
	configMap, err := c.CoreV1().ConfigMaps(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		framework.Failf("failed to get slo-controller-config %s/%s, got unexpected error: %v",
			namespace, name, err)
	}

	if errors.IsNotFound(err) {
		framework.Logf("slo-controller-config %s/%s does not exist, need create", namespace, name)
		newConfigMap, err := manifest.ConfigMapFromManifest("slocontroller/slo-controller-config.yaml")
		framework.ExpectNoError(err)

		newConfigMap.SetNamespace(namespace)
		newConfigMap.SetName(name)
		if newConfigMap.Data == nil {
			newConfigMap.Data = map[string]string{}
		}
		for k, v := range wanted {
			newConfigMap.Data[k] = v
		}

		created, err := c.CoreV1().ConfigMaps(namespace).Create(context.TODO(), newConfigMap, metav1.CreateOptions{})
		framework.ExpectNoError(err)
		framework.Logf("create slo-controller-config successfully, data: %+v", created.Data)

		return func() { rollbackSLOConfigObject(f, namespace, name) }
	}

	action := planSLOConfigData(configMap.Data, wanted)
	if !action.needUpdate {
		framework.Logf("slo-controller-config %s/%s already has the wanted data, keep the same",
			namespace, name)
		return func() {}
	}

	newConfigMap := configMap.DeepCopy()
	newConfigMap.Data = action.newData
	updated, err := c.CoreV1().ConfigMaps(namespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err)
	framework.Logf("update slo-controller-config successfully, data: %+v", updated.Data)

	return func() { restoreSLOConfigData(f, namespace, name, action) }
}

// restoreSLOConfigData puts back the Data that ensureSLOConfigData replaced.
func restoreSLOConfigData(f *framework.Framework, namespace, name string, action sloConfigAction) {
	configMap, err := f.ClientSet.CoreV1().ConfigMaps(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		// Something else removed it; there is nothing left to restore.
		framework.Logf("slo-controller-config %s/%s is gone, skip rollback", namespace, name)
		return
	}
	framework.ExpectNoError(err)

	newConfigMap := configMap.DeepCopy()
	if action.restoreNil {
		newConfigMap.Data = nil
	} else {
		if newConfigMap.Data == nil {
			// Something replaced Data while the spec ran; restoring into a nil
			// map is what panicked before this helper existed.
			newConfigMap.Data = map[string]string{}
		}
		for k, v := range action.restoreData {
			newConfigMap.Data[k] = v
		}
		for _, k := range action.removeKeys {
			delete(newConfigMap.Data, k)
		}
	}

	restored, err := f.ClientSet.CoreV1().ConfigMaps(namespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err)
	framework.Logf("finish rollback updating slo-controller-config, final data: %v", restored.Data)
}
