/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-09 @author yangwanjin
 */

package predictor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"hybrid/pkg/constants"

	"k8s.io/klog/v2"
)

type DownloadService struct {
	serverURL        string
	outputDir        string
	outputFileName   string
	downloadInterval time.Duration
	stopCh           chan struct{}
	httpClient       *http.Client
}

func NewDownloadService(url string, file string, interval time.Duration) *DownloadService {
	if file == "" {
		file = constants.DefaultPredictionFile
	}
	// 下载文件的周期大于控制器同步周期
	if interval <= 0 {
		interval = constants.DefaultSyncInterval / 2
	} else {
		interval = interval / 2
	}

	return &DownloadService{
		serverURL:        url,
		downloadInterval: interval,
		outputDir:        constants.DefaultOutputDir,
		outputFileName:   file,
		stopCh:           make(chan struct{}),
		httpClient: &http.Client{
			Timeout: time.Second * 30,
		},
	}
}

func (d *DownloadService) Start(ctx context.Context) error {
	klog.Infof("Starting download service, server: %s, interval: %v, output: %s/%s",
		d.serverURL, d.downloadInterval, d.outputDir, d.outputFileName)

	if err := os.MkdirAll(d.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	if err := d.download(ctx); err != nil {
		klog.Errorf("Initial download failed: %v", err)
	}

	ticker := time.NewTicker(d.downloadInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			klog.Info("Context cancelled, stopping download service")
			return nil
		case <-d.stopCh:
			klog.Info("Stop signal received, stopping download service")
			return nil
		case <-ticker.C:
			klog.V(4).Info("Ticker triggered, starting download")
			if err := d.download(ctx); err != nil {
				klog.Errorf("Download failed: %v", err)
			}
		}
	}
}

func (d *DownloadService) Stop() {
	close(d.stopCh)
}

func (d *DownloadService) Clear() {
	_ = os.RemoveAll(d.outputDir)
}
func (d *DownloadService) download(ctx context.Context) error {
	startTime := time.Now()
	url := d.serverURL + constants.DownloadEndpoint

	klog.V(4).Infof("Downloading from: %s", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	outputPath := filepath.Join(d.outputDir, d.outputFileName)
	tmpPath := outputPath + ".tmp"

	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		tmpFile.Close()
		if err != nil {
			os.Remove(tmpPath)
		}
	}()

	written, err := io.Copy(tmpFile, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("failed to rename file: %w", err)
	}

	duration := time.Since(startTime)
	klog.Infof("Successfully downloaded %d bytes to %s (took %v)", written, outputPath, duration)

	return nil
}
