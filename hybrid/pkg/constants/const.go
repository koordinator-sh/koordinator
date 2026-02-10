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

	DownloadEndpoint = "/v1/download-cluster-csv"
)
