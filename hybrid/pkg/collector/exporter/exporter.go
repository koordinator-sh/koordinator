/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-01-30 @author yangwanjin
 */

package exporter

import (
	"context"
	"fmt"
	"time"

	"k8s.io/klog/v2"

	config "hybrid/config/collector"
	"hybrid/pkg/collector/prometheus"
)

const (
	JsonFormat  = "json"
	ExcelFormat = "excel"
)

// Exporter prometheus metrics data exporter
type Exporter struct {
	config         *config.Config
	promClient     *prometheus.Client
	uploadNotifyCh chan string // notify upload ch
}

// NewExporter create a new exporter
func NewExporter(promClient *prometheus.Client, cfg *config.Config, notify chan string) *Exporter {
	return &Exporter{
		promClient:     promClient,
		config:         cfg,
		uploadNotifyCh: notify,
	}
}

// Export metrics data and notify upload service
func (e *Exporter) Export() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// save query results
	allResults := make(map[string][]prometheus.QueryResult)

	for i, queryConfig := range e.config.Queries {
		klog.Infof("Executing %s query %d/%d: %s", e.config.Export.Mode, i+1, len(e.config.Queries), queryConfig.Name)
		// execute query
		results, err := e.promClient.QueryRange(ctx, queryConfig.Query, queryConfig.Range, queryConfig.Step)
		if err != nil {
			klog.Errorf("Failed to query metrics for %s: %v", queryConfig.Name, err)
			continue
		}

		klog.Infof("Query %s returned %d time series", queryConfig.Name, len(results))
		allResults[queryConfig.Name] = results
	}

	var exportPath string
	var err error
	// handle export mode
	switch e.config.Export.Mode {
	case config.ExportModeLocal:
		exportPath, err = e.exportToFile(JsonFormat, allResults)
		if err != nil {
			return fmt.Errorf("failed to export metrics data to local file: %w", err)
		}
	case config.ExportModeRemote:
		// TODO: export to remote websocket server
		return fmt.Errorf("remote export mode not supported now")
	default:
		return fmt.Errorf("unknown export mode: %s", e.config.Export.Mode)
	}

	// notify upload after export
	if exportPath != "" && e.uploadNotifyCh != nil {
		select {
		case e.uploadNotifyCh <- exportPath:
			klog.Infof("Notified upload service: %s", exportPath)
		default:
			klog.Warning("Upload channel full, notification dropped")
		}
	}

	return nil

}

func (e *Exporter) exportToFile(fileType string, allResults map[string][]prometheus.QueryResult) (string, error) {
	switch fileType {
	case "json":
		return e.exportToJson(allResults)
	case "excel":
		return e.exportToExcel(allResults)
	default:
		return e.exportToJson(allResults)
	}
}
