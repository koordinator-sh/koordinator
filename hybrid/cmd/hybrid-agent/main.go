/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-09 @author yangwanjin
 */

package main

import (
	"os"

	"k8s.io/klog/v2"

	"hybrid/cmd/hybrid-agent/app"
)

func main() {
	cmd := app.NewHybridAgentCommand()
	if err := cmd.Execute(); err != nil {
		klog.ErrorS(err, "Failed to execute hybrid-agent")
		os.Exit(1)
	}
}
