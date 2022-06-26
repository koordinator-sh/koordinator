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

package docker

import (
	"context"
	"fmt"
	"net/http"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

func (d *RuntimeManagerDockerServer) generateErrorInfo(wr http.ResponseWriter, format string, args ...interface{}) {
	err := fmt.Errorf(format, args...)
	klog.Errorf("%v", err)
	http.Error(wr, err.Error(), http.StatusInternalServerError)
}

func (d *RuntimeManagerDockerServer) DockerRequestHandler(ctx context.Context, wr http.ResponseWriter, req *http.Request) {
	resExecutor := &PodResourceExecutorDocker{
		cgroupDriver: d.cgroupDriver,
	}
	if err := resExecutor.ParseRequest(req); err != nil {
		d.generateErrorInfo(wr, err.Error())
		return
	}
	defer resExecutor.DeleteCheckpointIfNeed(req)

	if resp, err := d.dispatcher.Dispatch(ctx, resExecutor.RuntimeRequestPath, config.PreHook, resExecutor.GenerateHookRequest()); err != nil {
		d.generateErrorInfo(wr, "failed to call pre start container hook server %v", err)
		return
	} else if err = resExecutor.ReConstructRequestByHookResponse(resp, req); err != nil {
		d.generateErrorInfo(wr, "failed to reconstruct request by hook server response %v", err)
		return
	}

	resp := d.Direct(wr, req)
	if err := resExecutor.ResourceCheckPoint(resp); err != nil {
		klog.Infof("fail to update: %v", err)
		return
	}

	if _, err := d.dispatcher.Dispatch(ctx, resExecutor.RuntimeRequestPath, config.PostHook, resExecutor.GenerateHookRequest()); err != nil {
		d.generateErrorInfo(wr, "failed to call post start container hook server %v", err)
		return
	}
}
