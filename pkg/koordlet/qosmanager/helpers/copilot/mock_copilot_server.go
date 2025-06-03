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

package copilot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

type YarnCopilotServer struct {
	unixPath string
}

func NewYarnCopilotServer(unixPath string) *YarnCopilotServer {
	return &YarnCopilotServer{unixPath: unixPath}
}

func (y *YarnCopilotServer) Run(ctx context.Context) error {
	e := gin.New()
	e.POST("/v1/killContainersByResource", y.KillContainersByResource)
	server := &http.Server{
		Handler: e,
	}
	sockDir := filepath.Dir(y.unixPath)
	_ = os.MkdirAll(sockDir, os.ModePerm)
	if system.FileExists(y.unixPath) {
		_ = os.Remove(y.unixPath)
	}
	listener, err := net.Listen("unix", y.unixPath)
	if err != nil {
		fmt.Printf("Failed to listen UNIX socket: %v", err)
		os.Exit(1)
	}
	defer func() {
		_ = os.Remove(y.unixPath)
	}()
	go func() {
		_ = server.Serve(listener)
	}()
	for range ctx.Done() {
		klog.Info("graceful shutdown")
		if err := server.Shutdown(ctx); err != nil {
			klog.Errorf("Server forced to shutdown: %v", err)
			return err
		}
	}
	return nil
}

func (y *YarnCopilotServer) KillContainersByResource(ctx *gin.Context) {
	var kr KillRequest
	if err := ctx.BindJSON(&kr); err != nil {
		ctx.JSON(http.StatusBadRequest, err)
		return
	}
	klog.Info("KillRequest: %s", kr)
	needReleasedCpu, _ := kr.Resources.Name(extension.BatchCPU, resource.DecimalSI).AsInt64()
	needReleasedMemory, _ := kr.Resources.Name(extension.BatchMemory, resource.BinarySI).AsInt64()
	//mock half of needReleasedResource
	mockReleasedCpu := needReleasedCpu / 2
	mockReleasedMemory := needReleasedMemory / 2
	releasedResourceList := v1.ResourceList{
		extension.BatchCPU:    *resource.NewQuantity(mockReleasedCpu, resource.DecimalSI),
		extension.BatchMemory: *resource.NewQuantity(mockReleasedMemory, resource.BinarySI),
	}
	klog.Infof("release resources: %s", releasedResourceList)
	//ctx.JSON(http.StatusOK, KillInfo{Items: []*ContainerInfo{ParseContainerInfo(container, y.mgr)}})
	ctx.JSON(http.StatusOK, releasedResourceList)
}
