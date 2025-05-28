// copilot_test.go
package copilot

import (
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"testing"
	"time"

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
		apiext.BatchCPU:    resource.MustParse("1k"),
		apiext.BatchMemory: resource.MustParse("2Gi"),
	}
	assert.Equal(t, expected, result)
}
