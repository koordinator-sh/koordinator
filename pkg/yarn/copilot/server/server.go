package server

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/koordinator-sh/koordinator/pkg/yarn/copilot/nm"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"net"
	"net/http"
	"os"
	"time"
)

type YarnCopilotServer struct {
	mgr      *nm.NodeMangerOperator
	unixPath string
}

func NewYarnCopilotServer(mgr *nm.NodeMangerOperator, unixPath string) *YarnCopilotServer {
	return &YarnCopilotServer{mgr: mgr}
}

func (y *YarnCopilotServer) Run(ctx context.Context) error {
	e := gin.New()
	e.GET("/v1/container", y.GetContainer)
	e.GET("/v1/containers", y.ListContainers)
	e.POST("/v1/killContainer", y.KillContainer)
	e.POST("/v1/killContainersByResource", y.KillContainerByResource)

	server := &http.Server{
		Handler: e,
	}
	listener, err := net.Listen("unix", y.unixPath)
	if err != nil {
		fmt.Printf("Failed to listen UNIX socket: %v", err)
		os.Exit(1)
	}
	defer os.Remove(y.unixPath)

	if err := server.Shutdown(ctx); err != nil {
		klog.Error("Server forced to shutdown: %v", err)
	}
	return server.Serve(listener)
}

func (y *YarnCopilotServer) ListContainers(ctx *gin.Context) {
	listContainers, err := y.mgr.ListContainers()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, err)
		return
	}
	res := make([]*ContainerInfo, 0, len(listContainers.Containers.Items))
	for _, container := range listContainers.Containers.Items {
		res = append(res, ParseContainerInfo(&container, y.mgr))
	}
	ctx.JSON(http.StatusOK, res)
}

func (y *YarnCopilotServer) GetContainer(ctx *gin.Context) {
	containerID := ctx.Query("containerID")
	container, err := y.mgr.GetContainer(containerID)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, err)
		return
	}
	ctx.JSON(http.StatusOK, ParseContainerInfo(container, y.mgr))
}

type KillRequest struct {
	ContainerID string          `json:"containerID,omitempty"`
	Resources   v1.ResourceList `json:"resources,omitempty"`
}

type KillInfo struct {
	Items []*ContainerInfo `json:"items,omitempty"`
}

type ContainerInfo struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	UID             string            `json:"uid"`
	Labels          map[string]string `json:"labels"`
	Annotations     map[string]string `json:"annotations"`
	CreateTimestamp time.Time         `json:"createTimestamp"`

	CgroupDir   string                  `json:"cgroupDir"`
	HostNetwork bool                    `json:"hostNetwork"`
	Resources   v1.ResourceRequirements `json:"resources"`
}

func (y *YarnCopilotServer) KillContainer(ctx *gin.Context) {
	var kr KillRequest
	if err := ctx.BindJSON(&kr); err != nil {
		ctx.JSON(http.StatusBadRequest, err)
		return
	}
	container, err := y.mgr.GetContainer(kr.ContainerID)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, err)
		return
	}
	if err := y.mgr.KillContainer(kr.ContainerID); err != nil {
		ctx.JSON(http.StatusBadRequest, err)
		return
	}
	ctx.JSON(http.StatusOK, KillInfo{Items: []*ContainerInfo{ParseContainerInfo(container, y.mgr)}})
}

func (y *YarnCopilotServer) KillContainerByResource(ctx *gin.Context) {
	return
}
