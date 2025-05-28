package copilot

import (
	"context"
	"encoding/json"
	"github.com/go-resty/resty/v2"
	corev1 "k8s.io/api/core/v1"
	"net"
	"net/http"
	"time"
)

type CopilotAgent struct {
	copilotServerEndpoint string
	copilotAgentClient    *resty.Client
}

func NewCopilotAgent(copilotServerEndpoint string) *CopilotAgent {
	return &CopilotAgent{
		copilotServerEndpoint: copilotServerEndpoint,
		copilotAgentClient:    initClient(copilotServerEndpoint),
	}
}

func (copilotAgent *CopilotAgent) KillContainerByResource(nodeCpuRatio float64, nodeMemoryRatio float64, releaseResource *corev1.ResourceList) corev1.ResourceList {
	var result corev1.ResourceList
	killReq := KillRequest{
		Resources:       *releaseResource,
		NodeCpuRatio:    nodeCpuRatio,
		NodeMemoryRatio: nodeMemoryRatio,
	}

	// 2. 序列化为 JSON
	reqBody, _ := json.Marshal(killReq)
	_, err := copilotAgent.copilotAgentClient.R().SetBody(reqBody).SetResult(&result).Post("/v1/killContainersByResource")
	if err != nil {
		return nil
	} else {
		return result
	}
}

func initClient(copilotServerEndpoint string) *resty.Client {
	// 创建自定义的 transport
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", copilotServerEndpoint)
		},
	}
	// 创建 resty 客户端
	client := resty.New()
	// 设置 transport
	client.SetTransport(transport).
		SetScheme("http")
	client.SetTimeout(30 * time.Second)
	return client
}

type KillRequest struct {
	ContainerID     string              `json:"containerID,omitempty"`
	Resources       corev1.ResourceList `json:"resources,omitempty"`
	NodeCpuRatio    float64             `json:"nodeCpuRatio,omitempty"`
	NodeMemoryRatio float64             `json:"nodeMemoryRatio,omitempty"`
}
