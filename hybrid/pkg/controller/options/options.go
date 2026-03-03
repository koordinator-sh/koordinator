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
)

// ManagerOptions groups all constructor parameters for Manager.
type ManagerOptions struct {
	Client            kubernetes.Interface
	DownloadService   *predictor.Service
	SyncInterval      time.Duration
	ExcludeNamespaces []string
	OutputDir         string

	// Context and Cancel are created by options.NewControllerManager so that
	// the Manager owns the signal-handling lifecycle.
	Context context.Context
	Cancel  context.CancelFunc
}
