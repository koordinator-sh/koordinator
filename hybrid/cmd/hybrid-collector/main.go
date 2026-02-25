/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-01-30 @author yangwanjin
 */

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/klog/v2"

	config "hybrid/config/collector"
	"hybrid/pkg/collector/exporter"
	"hybrid/pkg/collector/prometheus"
	"hybrid/pkg/collector/upload"
)

var (
	configPath = flag.String("config", "", "Assign path for config file. If empty, uses default locations.")
)

func main() {

	flag.Parse()

	server := os.Getenv("AI_SERVER")
	token := os.Getenv("AI_TOKEN")
	if server == "" || token == "" {
		klog.Errorf("AI_SERVER or AI_TOKEN env variable not set")
		return
	}

	cfg, err := loadConfig()
	if err != nil {
		klog.Errorf("Failed to load configuration: %v", err)
		return
	}

	cfg.Upload.URL = server
	cfg.Upload.Token = token

	// validate configuration
	if err := cfg.Validate(); err != nil {
		klog.Errorf("Failed to invalid configuration: %v", err)
		return
	}

	klog.Infof("Successfully loaded configuration, prometheus: %s, mode: %s, interval: %s",
		cfg.Prometheus.URL, cfg.Export.Mode, cfg.Export.Interval)

	// initialize prometheus client
	promClient, err := prometheus.NewClient(cfg.Prometheus)
	if err != nil {
		klog.Errorf("Failed to create prometheus client: %v", err)
		return
	}

	// create export dir
	if cfg.Export.Mode == config.ExportModeLocal {
		if err := os.MkdirAll(cfg.Export.LocalConfig.OutputDir, 0755); err != nil {
			klog.Errorf("Failed to create output directory: %v", err)
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// notify channel (Export -> Upload)
	uploadNotify := make(chan string, 10)
	uploadService := upload.NewService(cfg.Upload.URL, cfg.Upload.Token)
	uploadService.Start()
	defer uploadService.Stop()

	// start a goroutine notify upload service
	go forwardNotifications(ctx, uploadNotify, uploadService)

	// create exporter
	exp := exporter.NewExporter(promClient, cfg, uploadNotify)

	klog.Infof("Starting hybrid collector service...")
	if err := exp.Export(); err != nil {
		klog.Errorf("Failed to initial export: %v", err)
	}

	// setup periodic export
	ticker := time.NewTicker(cfg.Export.Interval)
	defer ticker.Stop()

	// setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// export metrics periodically
	for {
		select {
		case <-ticker.C:
			klog.Infof("Starting hybrid collector service periodic export...")
			if err := exp.Export(); err != nil {
				klog.Errorf("Failed to export metrics data: %v", err)
			}
		case <-sigChan:
			klog.Infof("Received shutdown signal, exiting...")
			return
		}
	}
}

// forwardNotifications forwards export notifications to upload service
func forwardNotifications(ctx context.Context, exportNotify <-chan string, uploadService *upload.Service) {
	for {
		select {
		case <-ctx.Done():
			return
		case path := <-exportNotify:
			uploadService.NotifyDataReady(path)
		}
	}
}

func loadConfig() (*config.Config, error) {
	if *configPath != "" {
		return config.LoadFromFile(*configPath)
	}
	return config.InitConfig()
}
