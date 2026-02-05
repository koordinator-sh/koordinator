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
)

type PrometheusJSONResult struct {
	Metric map[string]string `json:"metric"`
	Values [][]interface{}   `json:"values"` // [timestamp, value_string]
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
				Metric: seriesResult.Metric, // Directly assign the metric labels map
				Values: make([][]interface{}, len(seriesResult.Values)),
			}

			// Convert TimeValue slice to the required [[timestamp, value_string]] format
			for i, tv := range seriesResult.Values {
				jsonResult.Values[i] = []interface{}{
					tv.Timestamp.Unix(),         // Unix timestamp (int64)
					fmt.Sprintf("%g", tv.Value), // Value as a string, preserving precision
				}
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

	// Serialize the final data structure to json
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal json data: %w", err)
	}

	// Write the json bytes to the gzip writer
	if _, err := gzWriter.Write(jsonBytes); err != nil {
		return fmt.Errorf("failed to write compressed data to file %s: %w", data, err)
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
		filename = fmt.Sprintf("prometheus_export_%s.gz", now.Format("2006-01-02"))
	case "timestamp":
		fallthrough
	default:
		filename = fmt.Sprintf("prometheus_export_%s.gz", now.Format("20060102_150405"))
	}
	return filename
}
