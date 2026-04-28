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
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

// ParseClassifyFile reads the CSV at filePath and returns a map keyed by
// "<namespace>/<inferred-workload-name>".
//
// Deduplication strategy: the first pod record seen for a given workload wins.
// This is intentional — all pods of the same workload share one PredictedType.
func ParseClassifyFile(filePath string) (map[string]PodRecord, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open classify file %q: %w", filePath, err)
	}
	defer f.Close()
	return parseClassifyCSV(f)
}

func parseClassifyCSV(r io.Reader) (map[string]PodRecord, error) {
	cr := csv.NewReader(r)

	// Consume the header row (pod,namespace,cluster,pod_type,…)
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read classify CSV header: %w", err)
	}
	klog.V(5).InfoS("Classify CSV header", "columns", header)

	records := make(map[string]PodRecord)
	lineNum := 1

	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		lineNum++
		if err != nil {
			klog.V(4).InfoS("Skipping malformed CSV row", "line", lineNum, "err", err)
			continue
		}
		if len(row) < 4 {
			klog.V(4).InfoS("Skipping row: too few columns", "line", lineNum, "got", len(row))
			continue
		}

		cluster, _ := strconv.Atoi(strings.TrimSpace(row[2]))
		rec := PodRecord{
			Record: Record{
				Name:      strings.TrimSpace(row[0]),
				Namespace: strings.TrimSpace(row[1]),
				Cluster:   cluster,
			},
			PredictedType: strings.TrimSpace(row[3]),
		}

		if rec.Name == "" || rec.Namespace == "" {
			klog.V(4).InfoS("Skipping row: empty pod name or namespace", "line", lineNum)
			continue
		}

		// Derive the workload name so we can deduplicate per workload.
		workloadName := inferWorkloadName(rec.Name)
		if workloadName == "" {
			klog.V(4).InfoS("Skipping row: cannot infer workload name",
				"line", lineNum, "pod", rec.Name)
			continue
		}

		key := rec.Namespace + "/" + workloadName
		if _, exists := records[key]; !exists {
			records[key] = rec // first pod for this workload wins
		}
	}

	klog.V(4).InfoS("Parsed prediction file", "workloads", len(records))
	return records, nil
}

// inferWorkloadName strips Kubernetes pod-hash suffixes to recover the workload name.
//
// Known patterns:
//
//	Deployment:   <name>-<rs-hash>-<pod-hash>    → strip last 2 segments
//	StatefulSet:  <name>-<ordinal>               → strip last 1 segment (numeric)
//	DaemonSet/Job: <name>-<hash>                 → strip last 1 segment (hash)
func inferWorkloadName(podName string) string {
	parts := strings.Split(podName, "-")
	if len(parts) < 2 {
		return ""
	}

	last := parts[len(parts)-1]
	secondLast := parts[len(parts)-2]

	// Deployment: last two segments are both alphanumeric hashes.
	if len(parts) >= 3 && isKubeHash(last) && isKubeHash(secondLast) {
		return strings.Join(parts[:len(parts)-2], "-")
	}

	// StatefulSet: last segment is a pure integer ordinal (0, 1, 2, …).
	if isNumeric(last) {
		return strings.Join(parts[:len(parts)-1], "-")
	}

	// DaemonSet / Job: single hash suffix.
	if isKubeHash(last) {
		return strings.Join(parts[:len(parts)-1], "-")
	}

	return ""
}

// isKubeHash returns true for lowercase alphanumeric strings in [5, 10] chars,
// which matches Kubernetes-generated ReplicaSet and pod hash suffixes.
func isKubeHash(s string) bool {
	if len(s) < 5 || len(s) > 10 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// isNumeric returns true if every character in s is a digit.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
