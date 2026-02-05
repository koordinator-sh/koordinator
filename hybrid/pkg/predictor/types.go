/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-02 @author yangwanjin
 */

package predictor

// annotations
const (
	AnnotationPredictedType = "predictor.hybrid.sh/type"
	AnnotationTimestamp     = "predictor.hybrid.sh/timestamp"
)

type PodRecord struct {
	Name          string `json:"name"`
	Namespace     string `json:"namespace"`
	Cluster       string `json:"cluster"`
	PredictedType string `json:"predictedType"`
}
