/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-09 @author yangwanjin
 */

package constants

const ExportFilePrefix = "hybrid_export"

const (
	Model4ResultFilePrefix      = "model4_result"
	Model5ShortResultFilePrefix = "model5_short_result"
	Model5LongResultFilePrefix  = "model5_long_result"
	Model6ResultFilePrefix      = "model6_result"
)

// ModelResultsSubDir is the subdirectory under OutputDir where raw model
// result JSON files are saved (e.g. /data/hybrid-results/).
const ModelResultsSubDir = "hybrid-results"

const (
	DefaultOutputDir = "/data"

	// DefaultPredictionFile model4 预测结果,ClassifyController 读取
	DefaultPredictionFile = "classify-result.csv"

	// DefaultReplicaShortFile model5 短期副本预测结果,ReplicasController 读取
	DefaultReplicaShortFile = "replica-short-result.csv"

	// DefaultReplicaLongFile model5 长期副本预测结果(24h),ReplicasController 读取
	DefaultReplicaLongFile = "replica-long-result.csv"

	// DefaultInterferenceFile model6 干扰分析结果,InterferenceController 读取
	DefaultInterferenceFile = "interference-result.csv"
)

var DefaultExcludeNamespaces = []string{
	"kube-system",
	"kube-public",
	"kube-node-lease",
}
