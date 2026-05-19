/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-15 @author yangwanjin
 */

package predictor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hybrid/pkg/constants"
	"hybrid/pkg/simple/algorithm"

	"github.com/klauspost/compress/gzip"
	"k8s.io/klog/v2"
)

type FetchService struct {
	client      *algorithm.Client
	outputDir   string        // base dir for CSV downloads
	saveEnabled bool          // whether to persist raw JSON responses to disk
	resultsDir  string        // outputDir/hybrid-results; populated when saveEnabled
	retention   time.Duration // max age of files in resultsDir; 0 means use default (24h)
}

// NewFetchService creates a FetchService.
// When saveEnabled is true, raw JSON responses from the algorithm service are
// gzip-compressed and written to <outputDir>/hybrid-results/ with timestamp
// filenames.  Files older than retention are automatically deleted.
func NewFetchService(client *algorithm.Client, outputDir string, saveEnabled bool, retention time.Duration) *FetchService {
	svc := &FetchService{
		client:      client,
		outputDir:   outputDir,
		saveEnabled: saveEnabled,
		retention:   retention,
	}
	if saveEnabled && outputDir != "" {
		svc.resultsDir = filepath.Join(outputDir, constants.ModelResultsSubDir)
	}
	return svc
}

// saveResultJSON persists the raw JSON bytes returned by the algorithm service to a
// gzip-compressed file in resultsDir. The file naming follows the same timestamp
// convention used by the Prometheus exporter: <prefix>_20060102_150405.gz.
// Saving is skipped when saveEnabled is false. Failure is logged as a warning
// and does not affect the caller's return value.
func (f *FetchService) saveResultJSON(prefix string, data []byte) {
	if !f.saveEnabled || f.resultsDir == "" || len(data) == 0 {
		return
	}
	if err := os.MkdirAll(f.resultsDir, 0755); err != nil {
		klog.Warningf("FetchService: failed to create results dir %s: %v", f.resultsDir, err)
		return
	}
	go f.cleanup()

	filename := fmt.Sprintf("%s_%s.gz", prefix, time.Now().Format("20060102_150405"))
	path := filepath.Join(f.resultsDir, filename)

	file, err := os.Create(path)
	if err != nil {
		klog.Warningf("FetchService: failed to create result file %s: %v", path, err)
		return
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	if _, err := gz.Write(data); err != nil {
		klog.Warningf("FetchService: failed to write gzip data to %s: %v", path, err)
		return
	}
	if err := gz.Close(); err != nil {
		klog.Warningf("FetchService: failed to close gzip writer for %s: %v", path, err)
		return
	}
	klog.V(4).InfoS("Saved model result JSON", "file", path, "bytes", len(data))
}

// cleanup removes .gz files in resultsDir that are older than the configured
// retention period (default 24h).  It runs in a goroutine so it never blocks
// the caller.
func (f *FetchService) cleanup() {
	entries, err := os.ReadDir(f.resultsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			klog.Warningf("FetchService: failed to read results dir %s: %v", f.resultsDir, err)
		}
		return
	}

	retention := f.retention
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	cutoff := time.Now().Add(-retention)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(f.resultsDir, entry.Name())
			if err := os.Remove(path); err != nil {
				klog.Warningf("FetchService: failed to delete old result file %s: %v", path, err)
			} else {
				klog.V(4).InfoS("Deleted old model result file", "file", path)
			}
		}
	}
}

// FetchModel4Results calls the MODEL4 result API and parses the response.
func (f *FetchService) FetchModel4Results(ctx context.Context) ([]PodRecord, error) {
	var buf bytes.Buffer

	if err := f.client.Algorithm4Result(ctx, &buf); err != nil {
		return nil, fmt.Errorf("call model4 result API: %w", err)
	}

	f.saveResultJSON(constants.Model4ResultFilePrefix, buf.Bytes())

	var records []PodRecord
	if err := json.Unmarshal(buf.Bytes(), &records); err != nil {
		return nil, fmt.Errorf("parse model4 result JSON: %w", err)
	}

	klog.V(4).InfoS("Fetched model4 results", "count", len(records))
	return records, nil
}

// FetchModel5ShortResults calls the MODEL5 short-term result API and parses the response.
func (f *FetchService) FetchModel5ShortResults(ctx context.Context) ([]ReplicasShortRecord, error) {
	var buf bytes.Buffer

	if err := f.client.Algorithm5ShortResult(ctx, &buf); err != nil {
		return nil, fmt.Errorf("call model5 short result API: %w", err)
	}

	f.saveResultJSON(constants.Model5ShortResultFilePrefix, buf.Bytes())

	var records []ReplicasShortRecord
	if err := json.Unmarshal(buf.Bytes(), &records); err != nil {
		return nil, fmt.Errorf("parse model5 short result JSON: %w", err)
	}

	klog.V(4).InfoS("Fetched model5 short results", "count", len(records))
	return records, nil
}

// FetchModel5LongResults calls the MODEL5 long-term result API and parses the response.
func (f *FetchService) FetchModel5LongResults(ctx context.Context) ([]ReplicasLongRecord, error) {
	var buf bytes.Buffer

	if err := f.client.Algorithm5LongResult(ctx, &buf); err != nil {
		return nil, fmt.Errorf("call model5 long result API: %w", err)
	}

	f.saveResultJSON(constants.Model5LongResultFilePrefix, buf.Bytes())

	var records []ReplicasLongRecord
	if err := json.Unmarshal(buf.Bytes(), &records); err != nil {
		return nil, fmt.Errorf("parse model5 long result JSON: %w", err)
	}

	klog.V(4).InfoS("Fetched model5 long results", "count", len(records))
	return records, nil
}

// FetchModel6Results calls the MODEL6 result API and parses the response.
func (f *FetchService) FetchModel6Results(ctx context.Context) ([]InterferenceRecord, error) {
	var buf bytes.Buffer

	if err := f.client.Algorithm6Result(ctx, &buf); err != nil {
		return nil, fmt.Errorf("call model6 result API: %w", err)
	}

	f.saveResultJSON(constants.Model6ResultFilePrefix, buf.Bytes())

	var records []InterferenceRecord
	if err := json.Unmarshal(buf.Bytes(), &records); err != nil {
		return nil, fmt.Errorf("parse model6 result JSON: %w", err)
	}

	klog.V(4).InfoS("Fetched model6 results", "count", len(records))
	return records, nil
}
