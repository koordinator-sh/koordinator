/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-15 @author yangwanjin
 */

package predictor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hybrid/pkg/simple/algorithm"

	"k8s.io/klog/v2"
)

type FetchService struct {
	client *algorithm.Client
}

func NewFetchService(client *algorithm.Client) *FetchService {
	return &FetchService{
		client: client,
	}
}

// FetchModel4Results calls the MODEL4 result API and parses the response.
func (f *FetchService) FetchModel4Results(ctx context.Context) ([]PodRecord, error) {
	var buf bytes.Buffer

	// Call algorithm service result API
	if err := f.client.Algorithm4Result(ctx, &buf); err != nil {
		return nil, fmt.Errorf("call model4 result API: %w", err)
	}

	// Parse JSON response
	var records []PodRecord
	if err := json.Unmarshal(buf.Bytes(), &records); err != nil {
		return nil, fmt.Errorf("parse model4 result JSON: %w", err)
	}

	klog.V(4).InfoS("Fetched model4 results", "count", len(records))
	return records, nil
}

// FetchModel5ShortResults calls the MODEL5 short-term result API and parses the response.
func (f *FetchService) FetchModel5ShortResults(ctx context.Context) ([]ReplicasShortRecord, error) {
	var buf bytes.Buffer

	// Call algorithm service result API
	if err := f.client.Algorithm5ShortResult(ctx, &buf); err != nil {
		return nil, fmt.Errorf("call model5 short result API: %w", err)
	}

	// Parse JSON response
	var records []ReplicasShortRecord
	if err := json.Unmarshal(buf.Bytes(), &records); err != nil {
		return nil, fmt.Errorf("parse model5 short result JSON: %w", err)
	}

	klog.V(4).InfoS("Fetched model5 short results", "count", len(records))
	return records, nil
}

// FetchModel5LongResults calls the MODEL5 long-term result API and parses the response.
func (f *FetchService) FetchModel5LongResults(ctx context.Context) ([]ReplicasLongRecord, error) {
	var buf bytes.Buffer

	// Call algorithm service result API
	if err := f.client.Algorithm5LongResult(ctx, &buf); err != nil {
		return nil, fmt.Errorf("call model5 long result API: %w", err)
	}

	// Parse JSON response
	var records []ReplicasLongRecord
	if err := json.Unmarshal(buf.Bytes(), &records); err != nil {
		return nil, fmt.Errorf("parse model5 long result JSON: %w", err)
	}

	klog.V(4).InfoS("Fetched model5 long results", "count", len(records))
	return records, nil
}

// FetchModel6Results calls the MODEL6 result API and parses the response.
func (f *FetchService) FetchModel6Results(ctx context.Context) ([]InterferenceRecord, error) {
	var buf bytes.Buffer

	// Call algorithm service result API
	if err := f.client.Algorithm6Result(ctx, &buf); err != nil {
		return nil, fmt.Errorf("call model6 result API: %w", err)
	}

	// Parse JSON response
	var records []InterferenceRecord
	if err := json.Unmarshal(buf.Bytes(), &records); err != nil {
		return nil, fmt.Errorf("parse model6 result JSON: %w", err)
	}

	klog.V(4).InfoS("Fetched model6 results", "count", len(records))
	return records, nil
}
