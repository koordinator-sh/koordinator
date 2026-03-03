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

	"k8s.io/klog/v2"

	"hybrid/pkg/constants"
)

// Service downloads the prediction CSV from the AI server.
// It implements the Downloader interface.
type Service struct {
	client    *http.Client
	serverURL string
	token     string
	outputDir string
}

// NewDownloadService creates a Service with the given server URL, auth token, and local output directory.
func NewDownloadService(serverURL, token, outputDir string) *Service {
	if outputDir == "" {
		outputDir = constants.DefaultOutputDir
	}
	return &Service{
		serverURL: serverURL,
		token:     token,
		outputDir: outputDir,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Download fetches the latest prediction CSV and atomically replaces the local copy.
// It implements the Downloader interface.
func (s *Service) Download(ctx context.Context) error {
	if err := os.MkdirAll(s.outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory %q: %w", s.outputDir, err)
	}

	start := time.Now()
	endpoint := s.serverURL + constants.DownloadCSVEndpoint
	klog.V(4).InfoS("Downloading prediction file", "endpoint", endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-token", s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status %d from %s", resp.StatusCode, endpoint)
	}

	destPath := filepath.Join(s.outputDir, constants.DefaultPredictionFile)
	tmpPath := destPath + ".tmp"

	// Write to a temp file, then rename — prevents partial reads by the parser.
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	written, copyErr := io.Copy(tmpFile, resp.Body)
	closeErr := tmpFile.Close() // close explicitly before rename (Windows-safe)

	if copyErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write prediction file: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", closeErr)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replace prediction file: %w", err)
	}

	klog.InfoS("Prediction file downloaded", "bytes", written, "dest", destPath, "elapsed", time.Since(start))
	return nil
}
