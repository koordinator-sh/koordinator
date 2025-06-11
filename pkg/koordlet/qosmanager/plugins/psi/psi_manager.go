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

package psi

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/cgroup"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/operator"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/podcgroup"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

type PsiManager struct {
	podsLock sync.Mutex
	pods     map[types.UID]*podcgroup.PodCgroup
	ignored  map[types.UID]struct{}

	mask      cgroup.ResourceMask
	operators map[string]operator.Operator
}

func NewManager(mask cgroup.ResourceMask, ops ...operator.Operator) *PsiManager {
	pm := &PsiManager{
		pods:      make(map[types.UID]*podcgroup.PodCgroup),
		ignored:   make(map[types.UID]struct{}),
		mask:      mask,
		operators: make(map[string]operator.Operator),
	}
	pm.AddOperators(ops...)
	return pm
}

func (pm *PsiManager) AddOperators(ops ...operator.Operator) (retErr error) {
	pm.podsLock.Lock()
	defer pm.podsLock.Unlock()
	for _, op := range ops {
		if old, ok := pm.operators[op.Name()]; ok {
			oldSnapshot := jsonfy(old)
			if err := old.Update(op); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("failed to update %s: %w", op.Name(), err))
			}
			if newSnapshot := jsonfy(old); oldSnapshot != newSnapshot {
				klog.InfoS("Updated operator", "operator", op.Name(), "old", oldSnapshot, "new", newSnapshot)
			}
			continue
		}
		if err := op.Init(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to init %s: %w", op.Name(), err))
			continue
		}
		for _, pc := range pm.pods {
			if err := op.AddPod(pc); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("failed to add pod %s to %s: %w", klog.KObj(pc.Pod), op.Name(), err))
			}
		}
		pm.operators[op.Name()] = op
		klog.InfoS("Added operator", "operator", op.Name(), "config", jsonfy(op))
	}
	return retErr
}

func (pm *PsiManager) Batch(pods []*statesinformer.PodMeta) (retErr error) {
	pm.podsLock.Lock()
	defer pm.podsLock.Unlock()
	expired := make(map[types.UID]struct{})
	for _, pc := range pm.pods {
		expired[pc.Pod.UID] = struct{}{}
	}
	for _, meta := range pods {
		delete(expired, meta.Pod.UID)
		// TODO: provide device number to support io reconcile
		if err := pm.AddPod(meta.Pod, meta.CgroupDir, [2]int64{0, 0}); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to add pod %s: %w", klog.KObj(meta.Pod), err))
		}
	}
	for uid := range expired {
		if err := pm.DeletePod(uid); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to delete pod %s: %w", uid, err))
		}
	}
	return retErr
}

func (pm *PsiManager) AddPod(pod *v1.Pod, cgroupDir string, dev [2]int64) (retErr error) {
	if _, ok := pm.pods[pod.UID]; ok {
		pm.pods[pod.UID].Pod = pod
		return nil
	}
	if _, ok := pm.ignored[pod.UID]; ok {
		return nil
	}
	if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
		pm.ignored[pod.UID] = struct{}{}
		klog.InfoS("Skip finished pod", "pod", klog.KObj(pod))
		return nil
	}
	if pod.ResourceVersion == "" {
		pm.ignored[pod.UID] = struct{}{}
		klog.InfoS("Skip static pod", "pod", klog.KObj(pod))
		return nil
	}

	if cgroupDir == "" {
		return fmt.Errorf("pod has no cgroup path")
	}
	pc := &podcgroup.PodCgroup{
		Pod:    pod,
		Cgroup: cgroup.NewCgroup(filepath.Join(system.Conf.CgroupRootDir, cgroupDir), dev),
	}

	for _, po := range pm.operators {
		if err := po.AddPod(pc); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to add pod %s to %s: %w", klog.KObj(pc.Pod), po.Name(), err))
		}
	}
	pm.pods[pod.UID] = pc

	klog.InfoS("Added pod", "pod", klog.KObj(pod))
	return retErr
}

func (pm *PsiManager) DeletePod(uid types.UID) (retErr error) {
	if pc, ok := pm.pods[uid]; ok {
		for _, po := range pm.operators {
			if err := po.DeletePod(pc); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("failed to delete pod %s from %s: %w", klog.KObj(pc.Pod), po.Name(), err))
			}
		}
		klog.InfoS("Deleted pod", "pod", klog.KObj(pc.Pod))
	}
	delete(pm.pods, uid)
	delete(pm.ignored, uid)
	return retErr
}

func (pm *PsiManager) Reconcile(node *v1.Node) (err error) {
	pm.podsLock.Lock()
	defer pm.podsLock.Unlock()
	for _, pc := range pm.pods {
		if err := pc.Load(pm.mask); err != nil {
			klog.ErrorS(err, "Failed to load cgroup", "pod", klog.KObj(pc.Pod))
		}
	}

	errCh := make(chan error)
	for _, op := range pm.operators {
		go func(op operator.Operator) {
			if err := op.Exec(pm.pods, node); err != nil {
				errCh <- fmt.Errorf("failed to perform %s: %w", op.Name(), err)
			} else {
				errCh <- nil
			}
		}(op)
	}
	for range pm.operators {
		err = errors.Join(<-errCh)
	}
	return err
}

func jsonfy(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("failed to marshal %#v: %v", v, err)
	}
	return string(b)
}
