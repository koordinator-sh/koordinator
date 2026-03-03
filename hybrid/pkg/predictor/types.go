/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-02 @author yangwanjin
 */

// Package predictor handles downloading and parsing AI workload prediction results.
package predictor

import "context"

// Annotation keys written onto Kubernetes workloads.
const (
	// AnnotationPredictedType is the AI-assigned workload classification (e.g. "cpu-intensive").
	AnnotationPredictedType = "predictor.hybrid.sh/type"

	// AnnotationTimestamp records when hybrid-manager last wrote the annotation.
	AnnotationTimestamp = "predictor.hybrid.sh/timestamp"
)

// Downloader is the interface for fetching the latest prediction CSV from the AI server.
// Using an interface keeps the ClassifyController testable without a real HTTP server.
type Downloader interface {
	Download(ctx context.Context) error
}

// PodRecord represents a single row from the AI prediction CSV.
// Namespace/Name identify the source pod; PredictedType is the AI output.
type PodRecord struct {
	Name          string `csv:"pod"`
	Namespace     string `csv:"namespace"`
	Cluster       string `csv:"cluster"`
	PredictedType string `csv:"pod_type"`
}

// WorkloadResourceRecord will hold AI-predicted resource recommendations.
// TODO: add fields for cpu/memory request and limit predictions.
type WorkloadResourceRecord struct{}
