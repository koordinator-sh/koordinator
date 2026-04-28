/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-03-02 @author yangwanjin
 */

package options

import (
	"context"
	"time"

	"k8s.io/client-go/kubernetes"

	"hybrid/pkg/predictor"
	"hybrid/pkg/simple/algorithm"
)

// ManagerOptions groups all constructor parameters for Manager.
type ManagerOptions struct {
	Client            kubernetes.Interface
	DownloadService   *predictor.DownloadService
	FetchService      *predictor.FetchService
	SyncInterval      time.Duration
	ExcludeNamespaces []string
	OutputDir         string

	// Notifier 通过 Subscribe 订阅自己关心 model 的完成事件
	// Watcher 任务成功 → Notifier.notify → 控制器专属 channel → 触发 sync
	Notifier *algorithm.Notifier

	// Context and Cancel are created by options.NewControllerManager so that
	// the Manager owns the signal-handling lifecycle.
	Context context.Context
	Cancel  context.CancelFunc
}
