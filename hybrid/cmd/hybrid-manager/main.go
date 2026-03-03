/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-02 @author yangwanjin
 *
 */

package main

import (
	"os"

	"k8s.io/klog/v2"

	"hybrid/cmd/hybrid-manager/app"
)

func main() {
	cmd := app.NewHybridManagerCommand()
	if err := cmd.Execute(); err != nil {
		klog.ErrorS(err, "Failed to execute hybrid-manager")
		os.Exit(1)
	}
}
