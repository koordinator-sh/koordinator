/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-03 @author yangwanjin
 */

package predictor

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	"k8s.io/klog/v2"
)

func ParsePredictorFile(filePath string) (map[string]PodRecord, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open result file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)

	// read header and skip header
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read csv header: %w", err)
	}

	klog.V(4).Infof("Prediction result svc header: %v", header)

	records := make(map[string]PodRecord)
	lineNum := 1

	for {
		line, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			klog.Warningf("Error reading line %d: %v", lineNum, err)
			lineNum++
			continue
		}

		// svc header format: pod,namespace,cluster,pod_type,...
		if len(line) < 4 {
			klog.Warningf("Line %d has insufficient columns: %v", lineNum, line)
			lineNum++
			continue
		}

		record := PodRecord{
			Name:          line[0],
			Namespace:     line[1],
			Cluster:       line[2],
			PredictedType: line[3],
		}

		// if namespace is empty skip
		if record.Namespace == "" {
			klog.V(4).Infof("Skipping pod %s: because namespace is empty", record.Name)
			lineNum++
			continue
		}

		// if workload type is empty skip
		controllerName := getPodController(record.Name)
		if controllerName == "" {
			klog.V(4).Infof("Skipping pod %s: because controller name is empty", record.Name)
			lineNum++
			continue
		}

		workloadKey := fmt.Sprintf("%s/%s", record.Namespace, controllerName)
		if _, exists := records[workloadKey]; !exists {
			records[workloadKey] = record
		}
		lineNum++
	}

	klog.V(4).Infof("Successfully read %d records from csv file", len(records))
	return records, nil
}

func getPodController(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) >= 2 {
		return strings.Join(parts[:len(parts)-1], "-")
	}
	return ""
}
