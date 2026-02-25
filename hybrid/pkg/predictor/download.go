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

type Service struct {
	url       string
	token     string
	outputDir string
	fileName  string
	client    *http.Client
	stopCh    chan struct{}
}

func NewService(url, token string) *Service {
	return &Service{
		url:       url,
		token:     token,
		outputDir: constants.DefaultOutputDir,
		fileName:  constants.DefaultPredictionFile,
		client: &http.Client{
			Timeout: time.Second * 30,
		},
		stopCh: make(chan struct{}),
	}
}

func (d *Service) DownloadNow(ctx context.Context) error {
	if err := os.MkdirAll(d.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	return d.download(ctx)
}

func (d *Service) download(ctx context.Context) error {
	startTime := time.Now()
	endpoint := d.url + constants.DownloadCSVEndpoint

	klog.V(4).Infof("Downloading from: %s", endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("x-token", d.token)

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	outputPath := filepath.Join(d.outputDir, d.fileName)
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
