/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-09 @author yangwanjin
 */

package collector

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	config "hybrid/config/collector"
	"hybrid/pkg/collector/exporter"
	"hybrid/pkg/collector/upload"
	"hybrid/pkg/simple/algorithm"
	"hybrid/pkg/simple/prometheus"

	"k8s.io/klog/v2"
)

// Collector drives the Prometheus-export → upload → algorithm-trigger pipeline.
type Collector struct {
	ctx             context.Context
	wg              sync.WaitGroup
	config          *config.Config
	uploadService   *upload.Service
	exporterService *exporter.Exporter
	cancel          context.CancelFunc
}

// NewCollector constructs a Collector.
func NewCollector(ctx context.Context, cfg *config.Config, algoClient *algorithm.Client, watcher *algorithm.Watcher) (*Collector, error) {
	collectorCtx, cancel := context.WithCancel(ctx)

	// initialize prometheus client
	promClient, err := prometheus.NewClient(cfg.Prometheus)
	if err != nil {
		klog.Errorf("Failed to create prometheus client: %v", err)
		cancel()
		return nil, fmt.Errorf("create prometheus client: %w", err)
	}

	// Create upload service
	uploadService := upload.NewService(algoClient, watcher, collectorCtx)

	// Create exporter service
	exporterService := exporter.NewExporter(promClient, cfg, uploadService)

	col := &Collector{
		ctx:             collectorCtx,
		config:          cfg,
		exporterService: exporterService,
		uploadService:   uploadService,
		cancel:          cancel,
	}

	return col, nil
}

// Start starts the collector and blocks until the context is cancelled.
func (c *Collector) Start() error {
	c.run()
	return nil
}

// run is the main collector loop. It is NOT responsible for signal handling —
// the parent context (created with signal.NotifyContext in options.go) already
// handles SIGTERM/SIGINT and cancels c.ctx.
func (c *Collector) run() {
	klog.Info("Collector started")

	// create and ensure export dir
	if c.config.Export.Mode == config.ExportModeLocal {
		if err := os.MkdirAll(c.config.Export.LocalConfig.OutputDir, 0755); err != nil {
			klog.Errorf("Failed to create output directory: %v", err)
			return
		}
	}

	c.uploadService.Run()
	defer c.uploadService.Stop()

	// Initial export immediately on startup.
	klog.Infof("Starting initial metrics export...")
	if err := c.exporterService.Export(); err != nil {
		klog.Errorf("Failed to perform initial export: %v", err)
	}

	ticker := time.NewTicker(c.config.Export.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			klog.Info("Collector received shutdown signal, stopping")
			return
		case <-ticker.C:
			klog.V(4).Info("Starting periodic metrics export...")
			if err := c.exporterService.Export(); err != nil {
				klog.Errorf("Failed to export metrics data: %v", err)
			} else {
				klog.V(4).Info("Periodic export completed successfully")
			}
		}
	}
}

// Shutdown cancels the collector context and waits for the loop goroutine to exit.
func (c *Collector) Shutdown(ctx context.Context) error {
	klog.Info("Shutting down collector")
	c.cancel()

	// Wait for goroutines to finish
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("collector shutdown timed out: %w", ctx.Err())
	}
}
