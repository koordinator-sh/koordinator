/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-03 @author yangwanjin
 */

package algorithm

import (
	"fmt"
	"strings"
)

// MODEL4 负载分类算法
// MODEL5 负载均衡算法
// MODEL6 干扰分析算法

// ModelType 支持的算法模型
type ModelType string

const (
	Model4      ModelType = "MODEL4"
	Model5      ModelType = "MODEL5"       // 用于 upload/cleanup，触发短期+长期任务
	Model5Short ModelType = "MODEL5_SHORT" // 用于 watcher/notifier/controller，独立监听短期任务
	Model5Long  ModelType = "MODEL5_LONG"  // 用于 watcher/notifier/controller，独立监听长期任务
	Model6      ModelType = "MODEL6"

	StatusPending  = "PENDING"
	StatusProgress = "PROCESS"
	StatusSuccess  = "SUCCESS"
	StatusFailure  = "FAILURE"
	StatusRetry    = "RETRY"
	StatusDone     = "DONE"
)

var Models = []ModelType{Model4, Model5, Model6}

// ParseModels 将逗号分隔的模型列表(如 "MODEL4,MODEL6")解析为 []ModelType,
// 用于通过 --models 参数配置本次启用哪些模型进行数据推送/获取
func ParseModels(csv string) ([]ModelType, error) {
	valid := map[ModelType]bool{Model4: true, Model5: true, Model6: true}
	var models []ModelType
	for _, part := range strings.Split(csv, ",") {
		m := ModelType(strings.ToUpper(strings.TrimSpace(part)))
		if m == "" {
			continue
		}
		if !valid[m] {
			return nil, fmt.Errorf("unsupported model %q, must be one of MODEL4, MODEL5, MODEL6", m)
		}
		models = append(models, m)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("models list is empty")
	}
	return models, nil
}

type Result struct {
	Status  string `json:"status"`
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// UploadResponse 上传文件
type UploadResponse struct {
	TaskID   string      `json:"task_id"`
	Status   string      `json:"status"`
	Progress float64     `json:"progress"`
	Message  string      `json:"message"`
	Error    interface{} `json:"error"`
	Result   Result      `json:"result"`
}

// UploadStatusResponse 文件上传状态
type UploadStatusResponse struct {
	TaskID         string      `json:"task_id"`
	Status         string      `json:"status"`
	Progress       float64     `json:"progress"`
	Message        string      `json:"message"`
	UploadedBytes  int         `json:"uploaded_bytes"`
	TotalBytes     int         `json:"total_bytes"`
	ExtractedFiles int         `json:"extracted_files"`
	TotalFiles     int         `json:"total_files"`
	SkippedFiles   int         `json:"skipped_files"`
	Error          interface{} `json:"error"`
	Result         Result      `json:"result"`
}

// CleanResponse 清理模型对应的文件
type CleanResponse struct {
	Status        string      `json:"status"`
	Message       string      `json:"message"`
	Timestamp     string      `json:"timestamp"`
	CleanedData   bool        `json:"cleaned_data"`
	CleanedOutput bool        `json:"cleaned_output"`
	DataSize      int         `json:"data_size"`
	OutputSize    interface{} `json:"output_size"`
}

// ModelRunningResponse 模型运行结果
type ModelRunningResponse struct {
	TaskID         string      `json:"task_id"`
	Status         string      `json:"status"`
	Progress       float64     `json:"progress"`
	Message        string      `json:"message"`
	UploadedBytes  int         `json:"uploaded_bytes"`
	TotalBytes     int         `json:"total_bytes"`
	ExtractedFiles int         `json:"extracted_files"`
	TotalFiles     int         `json:"total_files"`
	SkippedFiles   int         `json:"skipped_files"`
	Error          interface{} `json:"error"`
	Result         Result      `json:"result"`
}

type ModelStatusResponse struct {
	TaskID              string      `json:"task_id"`
	Status              string      `json:"status"`
	Progress            float64     `json:"progress"`
	Message             string      `json:"message"`
	Result              Result      `json:"result"`
	Step                interface{} `json:"step"`
	PodCount            interface{} `json:"pod_count"`
	PerformanceDataFile interface{} `json:"performance_data_file"`
	MetricsFile         interface{} `json:"metrics_file"`
	ClusterOutputDir    interface{} `json:"cluster_output_dir"`
}
