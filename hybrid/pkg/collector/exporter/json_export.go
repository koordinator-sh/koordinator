/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-04 @author yangwanjin
 */

package exporter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/gzip"
	"k8s.io/klog/v2"

	"hybrid/pkg/constants"
	"hybrid/pkg/simple/prometheus"
)

type PrometheusJSONResult struct {
	Metric     map[string]string `json:"metric"`
	Timestamps []interface{}     `json:"timestamps"`
	Values     []interface{}     `json:"values"`
}

// exportToJson exports metrics data to a compressed json file (.gz).
func (e *Exporter) exportToJson(allResults map[string][]prometheus.QueryResult) (string, error) {
	// Generate filename with .gz extension
	filename := generateCompressFileName(e.config.Export.LocalConfig.Format)
	exportPath := filepath.Join(e.config.Export.LocalConfig.OutputDir, filename)

	// Create the target directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(exportPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create target directory: %w", err)
	}

	// Clean up old files before creating new
	go e.clear(e.config.Export.LocalConfig.OutputDir)

	// Prepare the final data structure to serialize
	var finalData []PrometheusJSONResult

	// Iterate through the queries defined in the config to maintain order and include only requested ones
	for _, queryConfig := range e.config.Queries {
		results, ok := allResults[queryConfig.Name]
		if !ok {
			klog.Warningf("No results found for query: %s", queryConfig.Name)
			continue // Skip if query had errors or no results
		}

		// Process each time series returned by the query
		for _, seriesResult := range results {
			jsonResult := PrometheusJSONResult{
				Metric:     seriesResult.Metric, // Directly assign the metric labels map
				Timestamps: make([]interface{}, len(seriesResult.Values)),
				Values:     make([]interface{}, len(seriesResult.Values)),
			}

			// Convert TimeValue
			for i, tv := range seriesResult.Values {
				// Unix timestamp (int64)
				jsonResult.Timestamps[i] = tv.Timestamp.Unix()
				// Value as a string, preserving precision
				jsonResult.Values[i] = tv.Value
			}

			// 获取的数据至少保证两个数据点
			if len(jsonResult.Timestamps) < 2 || len(jsonResult.Values) < 2 {
				continue // Skip if illegal values
			}

			// 清洗全零数据,减少数据干扰
			allZero := true
			for _, v := range jsonResult.Values {
				if v != float64(0) {
					allZero = false
					break
				}
			}
			if allZero {
				continue // Skip if all values are zero
			}

			finalData = append(finalData, jsonResult)
		}
	}

	if len(finalData) == 0 {
		return "", fmt.Errorf("no metrics data to export")
	}

	if err := compressToGz(exportPath, finalData); err != nil {
		return "", fmt.Errorf("failed to compress prometheus export metrics data: %w", err)
	}

	klog.Infof("Successfully exported metrics data to compressed json file: %s", exportPath)
	return exportPath, nil
}

func compressToGz(path string, data []PrometheusJSONResult) error {
	// Create the .gz file
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", path, err)
	}
	defer file.Close()

	// Wrap the file with a buffered writer for efficiency
	bufWriter := bufio.NewWriter(file)

	// Create a gzip writer
	gzWriter := gzip.NewWriter(bufWriter)

	encoder := json.NewEncoder(gzWriter)

	// 逐行写入,每个对象一行(.jsonl 格式)
	for _, item := range data {
		if err := encoder.Encode(item); err != nil {
			return fmt.Errorf("failed to encode json line: %w", err)
		}
	}

	// Close gzip writer first to flush and write gzip footer
	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}

	// Then flush the buffered writer
	if err := bufWriter.Flush(); err != nil {
		return fmt.Errorf("failed to flush buffer: %w", err)
	}

	return nil
}

func (e *Exporter) clear(exportDir string) {
	// Check if output directory exists
	info, err := os.Stat(exportDir)
	if os.IsNotExist(err) {
		return // Directory doesn't exist, nothing to clean
	}
	if err != nil {
		klog.Errorf("failed to stat output directory: %s", err)
	}

	if !info.IsDir() {
		klog.Warningf("output path is not a directory: %s", exportDir)
	}

	// Read all files in the directory
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		klog.Errorf("failed to read output directory: %s", err)
	}

	var filesToDelete []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue // skip subdirectories
		}

		fileInfo, err := entry.Info()
		if err != nil {
			klog.Warningf("Failed to get file info for %s: %v", entry.Name(), err)
			continue
		}

		// Check if file matches expected pattern
		if e.shouldCleanFile(entry.Name()) {
			// Check if file should be deleted based on age
			if e.shouldDeleteFile(fileInfo) {
				filesToDelete = append(filesToDelete, filepath.Join(exportDir, entry.Name()))
			}
		}
	}

	// delete old files
	for _, filePath := range filesToDelete {
		if err := os.Remove(filePath); err != nil {
			klog.Warningf("Failed to delete old file %s: %v", filePath, err)
		} else {
			klog.Infof("Deleted old export file: %s", filePath)
		}
	}

}

func (e *Exporter) shouldCleanFile(filename string) bool {
	expectedExtensions := []string{".json.gz", ".tar.gz", ".gz"}
	for _, ext := range expectedExtensions {
		if strings.HasSuffix(strings.ToLower(filename), ext) {
			return true
		}
	}
	baseName := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if strings.Contains(baseName, constants.ExportFilePrefix) {
		return true
	}

	return false
}

// shouldDeleteFile determines if a file should be deleted based on age
func (e *Exporter) shouldDeleteFile(fileInfo os.FileInfo) bool {
	retentionHours := e.getRetentionHours()
	if retentionHours <= 0 {
		return false // No retention limit set
	}

	maxAge := time.Duration(retentionHours) * time.Hour
	currentTime := time.Now()
	fileModTime := fileInfo.ModTime()

	age := currentTime.Sub(fileModTime)
	return age > maxAge
}

func (e *Exporter) getRetentionHours() int64 {
	if e.config.Export.LocalConfig.RetentionHours > 0 {
		return int64(e.config.Export.LocalConfig.RetentionHours / time.Hour)
	}
	// default retain 12 hour export data
	return 12
}

func generateCompressFileName(format string) string {
	now := time.Now()
	var filename string
	switch format {
	case "daily":
		filename = fmt.Sprintf("%s_%s.gz", constants.ExportFilePrefix, now.Format("2006-01-02"))
	case "timestamp":
		fallthrough
	default:
		filename = fmt.Sprintf("%s_%s.gz", constants.ExportFilePrefix, now.Format("20060102_150405"))
	}
	return filename
}
