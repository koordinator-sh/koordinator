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

package reconciler

import (
	"reflect"
	"sync"
	"time"

	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

var globalHostAppReconcilers = struct {
	hostApps []*hostAppReconciler
}{}

type hostAppReconciler struct {
	resourceFile system.Resource
	description  string
	fn           reconcileFunc
}

func RegisterHostAppReconciler(resource system.Resource, description string, fn reconcileFunc, opt *ReconcilerOption) {
	for _, r := range globalHostAppReconcilers.hostApps {
		if resource.ResourceType() == r.resourceFile.ResourceType() {
			klog.Fatal("%v file already registered by %v", resource.ResourceType(), r.description)
		}
	}

	r := &hostAppReconciler{
		resourceFile: resource,
		description:  description,
		fn:           fn,
	}
	globalHostAppReconcilers.hostApps = append(globalHostAppReconcilers.hostApps, r)
}

type ReconcilerOption struct {
	// TODO mv filter and condition
}

type hostReconciler struct {
	appMutex          sync.RWMutex
	hostAppMap        map[string]*slov1alpha1.HostApplicationSpec
	appUpdated        chan struct{}
	executor          resourceexecutor.ResourceUpdateExecutor
	reconcileInterval time.Duration
}

func NewHostAppReconciler(ctx Context) Reconciler {
	r := &hostReconciler{
		appUpdated:        make(chan struct{}, 1),
		executor:          ctx.Executor,
		reconcileInterval: ctx.ReconcileInterval,
	}
	ctx.StatesInformer.RegisterCallbacks(statesinformer.RegisterTypeNodeSLOSpec, "host-app-reconciler",
		"Reconcile cgroup files if host app updated", r.appRefreshCallback)
	return r
}

func (r *hostReconciler) Run(stopCh <-chan struct{}) error {
	go r.doHostAppCgroup(stopCh)
	go r.reconcile(stopCh)
	klog.V(1).Infof("start host application reconciler successfully")
	return nil
}

func (r *hostReconciler) reconcile(stopCh <-chan struct{}) {
	timer := time.NewTimer(r.reconcileInterval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			if len(r.appUpdated) == 0 {
				r.appUpdated <- struct{}{}
				klog.V(5).Infof("reconcile host application with %v interval", r.reconcileInterval.String())
			}
			timer.Reset(r.reconcileInterval)
		case <-stopCh:
			klog.V(1).Infof("stop reconcile for host application")
		}
	}
}

func (r *hostReconciler) appRefreshCallback(t statesinformer.RegisterType, mergedNodeSLOSpecIf interface{},
	target *statesinformer.CallbackTarget) {
	if target == nil {
		klog.Warningf("callback target is nil")
		return
	}
	updated := r.parseHostApp(target.HostApplications)
	if !updated {
		klog.V(4).Infof("host application in node slo is not updated, no need to reconcile")
		return
	}
	if len(r.appUpdated) == 0 {
		r.appUpdated <- struct{}{}
	}
}

func (r *hostReconciler) parseHostApp(hostApps []slov1alpha1.HostApplicationSpec) bool {
	newHostAppMap := make(map[string]*slov1alpha1.HostApplicationSpec, len(hostApps))
	for _, app := range hostApps {
		newHostAppMap[app.Name] = app.DeepCopy()
	}
	updated := r.updateHostApp(newHostAppMap)
	klog.V(4).Infof("host application updated=%v with new %v", updated, newHostAppMap)
	return updated
}

func (r *hostReconciler) updateHostApp(hostAppMap map[string]*slov1alpha1.HostApplicationSpec) bool {
	r.appMutex.Lock()
	defer r.appMutex.Unlock()
	if !reflect.DeepEqual(hostAppMap, r.hostAppMap) {
		r.hostAppMap = hostAppMap
		return true
	}
	return false
}

func (r *hostReconciler) getHostApps() map[string]*slov1alpha1.HostApplicationSpec {
	r.appMutex.RLock()
	defer r.appMutex.RUnlock()
	result := make(map[string]*slov1alpha1.HostApplicationSpec, len(r.hostAppMap))
	for k, v := range r.hostAppMap {
		result[k] = v.DeepCopy()
	}
	return result
}

func (r *hostReconciler) doHostAppCgroup(stopCh <-chan struct{}) {
	for {
		select {
		case <-r.appUpdated:
			hostApps := r.getHostApps()
			for name, app := range hostApps {
				for _, appReconciler := range globalHostAppReconcilers.hostApps {
					hostCtx := protocol.HooksProtocolBuilder.HostApp(app)
					if err := appReconciler.fn(hostCtx); err != nil {
						klog.Warningf("calling host reconcile function %v failed, erro %v", appReconciler.description, err)
					} else {
						hostCtx.ReconcilerDone(r.executor)
						klog.V(5).Infof("calling host reconcile function %v for app %v finished", appReconciler.description, name)
					}
				}
			}
		case <-stopCh:
			klog.V(1).Infof("stop reconcile host app cgroup")
			return
		}
	}
}
