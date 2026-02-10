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
	"time"

	"github.com/klauspost/compress/gzip"
	"k8s.io/klog/v2"

	"hybrid/pkg/collector/prometheus"
	"hybrid/pkg/constants"
)

type PrometheusJSONResult struct {
	Metric     map[string]string `json:"metric"`
	Timestamps []interface{}     `json:"timestamps"`
	Values     []interface{}     `json:"values"`
}

// exportToJson exports metrics data to a compressed json file (.gz).
func (e *Exporter) exportToJson(allResults map[string][]prometheus.QueryResult) error {
	// Generate filename with .gz extension
	filename := generateCompressFileName(e.config.Export.LocalConfig.Format)
	exportPath := filepath.Join(e.config.Export.LocalConfig.OutputDir, filename)

	// Create the target directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(exportPath), 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

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
			finalData = append(finalData, jsonResult)
		}
	}

	err := compressToGz(exportPath, finalData)
	if err != nil {
		return fmt.Errorf("failed to compress prometheus export: %w", err)
	}

	klog.Infof("Successfully exported metrics data to compressed JSON file: %s", exportPath)
	return nil
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
	defer bufWriter.Flush()

	// Create a gzip writer
	gzWriter := gzip.NewWriter(bufWriter)
	defer gzWriter.Close()

	encoder := json.NewEncoder(gzWriter)

	// 逐行写入,每个对象一行(.jsonl 格式)
	for _, item := range data {
		if err := encoder.Encode(item); err != nil {
			return fmt.Errorf("failed to encode json line: %w", err)
		}
	}

	return nil
}

func compressTo7z(path string, data *[]PrometheusJSONResult) error {
	// todo:
	return nil
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
