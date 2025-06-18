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
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
	corev1 "k8s.io/api/core/v1"
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
