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

	// AnnotationReplicaShort model5 set short replica count (30min)
	AnnotationReplicaShort = "predictor.hybrid.sh/replicas-short"

	// AnnotationReplicaLong model5 set long replica count (24h)
	AnnotationReplicaLong = "predictor.hybrid.sh/replicas-long"

	// AnnotationInterference model6 set interference
	AnnotationInterference = "predictor.hybrid.sh/interference"

	// AnnotationTimestamp records when controller last wrote the annotation.
	AnnotationTimestamp = "predictor.hybrid.sh/timestamp"
)

// Downloader is the interface for fetching the latest prediction CSV from the AI server.
type Downloader interface {
	// Download fetches all model results. Use for full refresh scenarios.
	Download(ctx context.Context) error

	// DownloadModel4 fetches only the classify result (MODEL4).
	DownloadModel4(ctx context.Context) error

	// DownloadModel5 fetches only the replica prediction results (MODEL5 short + long).
	DownloadModel5(ctx context.Context) error

	// DownloadModel6 fetches only the interference analysis result (MODEL6).
	DownloadModel6(ctx context.Context) error
}

// Fetcher is the interface for fetching prediction results from the AI server.
// taskID 为触发本次 sync 的任务 ID；当 FetchService.useTaskID=true 时会附加到请求 URL，
// 确保多集群场景下只取本集群的结果。传空串时退回为取最新结果。
type Fetcher interface {
	// FetchModel4Results fetches the classify result for the given taskID.
	FetchModel4Results(ctx context.Context, taskID string) ([]PodRecord, error)

	// FetchModel5ShortResults fetches the short-term replica prediction result for the given taskID.
	FetchModel5ShortResults(ctx context.Context, taskID string) ([]ReplicasShortRecord, error)

	// FetchModel5LongResults fetches the long-term replica prediction result for the given taskID.
	FetchModel5LongResults(ctx context.Context, taskID string) ([]ReplicasLongRecord, error)

	// FetchModel6Results fetches the interference analysis result for the given taskID.
	FetchModel6Results(ctx context.Context, taskID string) ([]InterferenceRecord, error)
}

type Record struct {
	Name      string `csv:"name" json:"name,omitempty"`
	Namespace string `csv:"namespace" json:"namespace,omitempty"`
	Cluster   int    `csv:"cluster" json:"cluster,omitempty"`
}

// PodRecord represents a single row from the AI prediction CSV.
// Namespace/Name identify the source pod; PredictedType is the AI output.
type PodRecord struct {
	// Record this Record.Name is Pod name
	Record
	PredictedType string `csv:"pod_type" json:"pod_type,omitempty"`
}

type ReplicasShortRecord struct {
	// Record this Record.Name is WorkLoad name
	Record
	CurrentReplicas string `csv:"current_replicas" json:"current_replicas,omitempty"`
	ReplicaCount    string `csv:"recommend_replicas" json:"recommend_replicas,omitempty"`
	TotalReplicas   string `csv:"total_replicas" json:"total_replicas,omitempty"`
	PredictedAt     string `csv:"predicted_at" json:"predicted_at,omitempty"`
}

type ReplicasLongRecord struct {
	// Record this Record.Name is WorkLoad name
	Record
	CurrentReplicas   string `csv:"current_replicas" json:"current_replicas,omitempty"`
	RecommendReplicas string `csv:"recommend_replicas" json:"recommend_replicas,omitempty"`
	TotalReplicas     string `csv:"total_replicas" json:"total_replicas,omitempty"`
	PredictedAt       string `csv:"predicted_at" json:"predicted_at,omitempty"`
}

// WorkloadResourceRecord set workload resource config
// TODO: add fields for cpu/memory request and limit predictions.
type WorkloadResourceRecord struct {
	// Record this Record.Name is WorkLoad name
	Record
	// TODO:
}

// InterferenceRecord will hold AI-predicted resource recommendations.
type InterferenceRecord struct {
	// Record this Record.Name is Pod name
	Record
	// InterferenceType 干扰类型: (eg. CPU竞争 / 内存压力)
	InterferenceType string `csv:"interference_type" json:"interference_type,omitempty"`
	// InterferenceLevel 干扰级别: (eg. CRIT / WARN / NONE)
	InterferenceLevel string `csv:"interference_level" json:"interference_level,omitempty"`
	// InterferenceSignal 触发信号:
	InterferenceSignal string `csv:"interference_signal" json:"interference_signal,omitempty"`
	// InterferenceReason 干扰原因: (eg. )
	InterferenceReason string `csv:"interference_reason" json:"interference_reason,omitempty"`
	// RecommendAction 建议动作: (eg. 增加资源限制 / 迁移节点)
	RecommendAction string `csv:"recommend_action" json:"recommend_action,omitempty"`
}
