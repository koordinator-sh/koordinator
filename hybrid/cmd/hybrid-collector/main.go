/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-01-30 @author yangwanjin
 */

package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/klog/v2"

	config "hybrid/config/collector"
	"hybrid/pkg/collector/exporter"
	"hybrid/pkg/collector/prometheus"
)

var (
	configPath = flag.String("config", "", "Path to config file. If empty, uses default locations.")
)

func main() {
	flag.Parse()
	var cfg *config.Config
	var err error

	// load configuration
	if *configPath != "" {
		klog.Infof("Loading config from specified file: %s", *configPath)
		cfg, err = config.LoadFromFile(*configPath)
		if err != nil {
			klog.Errorf("Failed to load config from file '%s': %v", *configPath, err)
			return
		}
	} else {
		klog.Infof("Loading config from default location (e.g. /etc/hybrid/config/collector.yaml)")
		cfg, err = config.InitConfig()
		if err != nil {
			klog.Errorf("Failed to load config using default method: %v", err)
			return
		}
	}

	// validate configuration
	if err := cfg.Validate(); err != nil {
		klog.Errorf("Invalid configuration: %v", err)
		return
	}

	klog.Infof("Loaded Config Successfully, prometheus URL: %s, export mode: %s, export interval: %s",
		cfg.Prometheus.URL, cfg.Export.Mode, cfg.Export.Interval)

	// create prometheus client
	promClient := prometheus.NewClient(cfg.Prometheus)

	// export metrics to local file
	if cfg.Export.Mode == config.ExportModeLocal {
		klog.Infof("Output directory: %s", cfg.Export.LocalConfig.OutputDir)
		if err := os.MkdirAll(cfg.Export.LocalConfig.OutputDir, 0755); err != nil {
			klog.Errorf("Failed to create output directory: %v", err)
			return
		}
	}

	// create exporter
	exp := exporter.NewExporter(promClient, cfg)

	// setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// setup periodic export
	ticker := time.NewTicker(cfg.Export.Interval)
	defer ticker.Stop()

	klog.Infof("Starting initial export...")
	if err := exp.Export(); err != nil {
		klog.Errorf("Initial export failed: %v", err)
	}

	// export metrics periodically
	for {
		select {
		case <-ticker.C:
			klog.Infof("Starting scheduled export...")
			if err := exp.Export(); err != nil {
				klog.Errorf("Export failed: %v", err)
			}
		case <-sigChan:
			klog.Infof("Received shutdown signal, exiting...")
			return
		}
	}
}
