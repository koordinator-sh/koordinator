/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-20 @author yangwanjin
 */

package algorithm

import (
	"context"
	"fmt"
	"time"

	"k8s.io/klog/v2"
)

const (
	// healthCheckTimeout 单次健康检查的超时时间
	healthCheckTimeout = 10 * time.Second

	// healthCheckInitialBackoff 首次重试等待时间
	healthCheckInitialBackoff = 5 * time.Second

	// healthCheckMaxBackoff 最大等待时间
	healthCheckMaxBackoff = 180 * time.Second
)

// CheckAndClean 调用 AI server 的模型清理接口,检查服务可用性 + 清理历史数据
// 1. 验证服务连通性与鉴权是否正常
// 2. 清理过往遗留的模型数据,避免脏数据干扰本轮运行
func (c *Client) CheckAndClean(ctx context.Context) error {
	for _, model := range Models {
		checkCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
		resp, err := c.CleanDirectories(checkCtx, model, model)
		cancel()

		if err != nil {
			return fmt.Errorf("AI server health check failed [model=%s]: %w", model, err)
		}
		klog.V(4).InfoS("AI server clean succeeded", "model", model, "status", resp.Status, "cleanedData", resp.CleanedData, "cleanedOutput", resp.CleanedOutput)
	}
	return nil
}

// WaitUntilReady 阻塞调用, 持续重试 CheckAndClean 直到服务可用或 context 被取消
// 使用指数退避策略(5s → 10s → 20s ... 最大是 180s)
//
// 阻塞数据导出、数据上传、控制器更新等流程, 确保 Agent 在模型服务就绪前不执行任何业务逻辑
func (c *Client) WaitUntilReady(ctx context.Context) error {
	klog.Info("Waiting for AI server to become ready...")
	backoff := healthCheckInitialBackoff

	for {
		err := c.CheckAndClean(ctx)
		if err == nil {
			klog.Info("AI server is ready, proceeding")
			return nil
		}

		klog.Errorf("AI server not ready: %v, retrying in %v", err, backoff)

		select {
		case <-ctx.Done():
			return fmt.Errorf("AI server readiness check aborted: %w", ctx.Err())
		case <-time.After(backoff):
			// 指数退避,上限 180s
			backoff *= 2
			if backoff > healthCheckMaxBackoff {
				backoff = healthCheckMaxBackoff
			}
		}
	}
}
