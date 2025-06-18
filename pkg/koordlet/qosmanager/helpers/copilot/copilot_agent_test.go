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

// copilot_test.go
package copilot

import (
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestKillContainerByResource(t *testing.T) {
	// 1. 配置 Socket 路径
	socketPath := "/tmp/copilot-test.sock"
	// 2. 启动测试服务端
	yarnCopilotServer := NewYarnCopilotServer(socketPath)
	go yarnCopilotServer.Run(signals.SetupSignalHandler())

	// 3. 初始化客户端
	agent := NewCopilotAgent(socketPath)
	releaseResource := &corev1.ResourceList{
		apiext.BatchCPU:    resource.MustParse("1k"),
		apiext.BatchMemory: resource.MustParse("2Gi"),
	}

	// 4. 等待服务端就绪
	time.Sleep(100 * time.Millisecond)

	// 5. 调用方法并验证结果
	result := agent.KillContainerByResource(0.8, 0.7, releaseResource)
	expected := corev1.ResourceList{
		apiext.BatchCPU:    resource.MustParse("500"),
		apiext.BatchMemory: resource.MustParse("1Gi"),
	}
	assert.Equal(t, expected, result)
}
