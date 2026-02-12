/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-09 @author yangwanjin
 */

package constants

import "time"

const ExportFilePrefix = "hybrid_export"

const (
	DefaultOutputDir      = "/data"
	DefaultPredictionFile = "prediction-result.csv"
	DefaultSyncInterval   = 5 * time.Minute

	DownloadCSVEndpoint  = "/v1/v1/download-cluster-csv"
	UploadFileEndpoint   = "/v1/v1/upload-file"
	UploadStatusEndpoint = "/v1/v1/upload-status"
	UploadCleanEndpoint  = "/v1/v1/clean-directories"
)
