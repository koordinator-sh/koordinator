/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-01-30 @author yangwanjin
 */

package prometheus

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"k8s.io/klog/v2"

	"hybrid/config/collector"
)

// Client prometheus client
type Client struct {
	api    v1.API
	config collector.PrometheusConfig
}

// QueryResult prometheus query result
type QueryResult struct {
	Metric map[string]string
	Values []TimeValue
}

// TimeValue prometheus time series value
type TimeValue struct {
	Timestamp time.Time
	Value     float64
}

// NewClient create prometheus client
func NewClient(cfg collector.PrometheusConfig) *Client {
	client, err := api.NewClient(api.Config{
		Address: cfg.URL,
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create Prometheus client: %v", err))
	}

	return &Client{
		api:    v1.NewAPI(client),
		config: cfg,
	}
}

// QueryRange execute a range query
func (c *Client) QueryRange(ctx context.Context, query, rangeStr, step string) ([]QueryResult, error) {
	// parse query time range
	duration, err := time.ParseDuration(rangeStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse query time range: %w", err)
	}

	// parse query step duration
	stepDuration, err := time.ParseDuration(step)
	if err != nil {
		return nil, fmt.Errorf("failed to parse query step duration: %w", err)
	}

	// set query time range
	end := time.Now()
	start := end.Add(-duration)

	// execute range query
	result, warnings, err := c.api.QueryRange(ctx, query, v1.Range{
		Start: start,
		End:   end,
		Step:  stepDuration,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to query metrics from prometheus: %w", err)
	}
	if len(warnings) > 0 {
		for _, w := range warnings {
			klog.Warningf("QueryRange warning: %s", w)
		}
	}

	// parse query result
	return c.parseResult(result), nil
}

// Query execute a query
func (c *Client) Query(ctx context.Context, query string) ([]QueryResult, error) {
	result, warnings, err := c.api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics from prometheus: %w", err)
	}

	if len(warnings) > 0 {
		for _, w := range warnings {
			klog.Warningf("Query warning: %s", w)
		}
	}

	return c.parseResult(result), nil
}

// parseResult parse query result
func (c *Client) parseResult(value model.Value) []QueryResult {
	var results []QueryResult

	switch v := value.(type) {
	case model.Matrix:
		// prometheus matrix query result
		for _, stream := range v {
			result := QueryResult{
				Metric: make(map[string]string),
				Values: make([]TimeValue, 0, len(stream.Values)),
			}

			// parse metric labels
			for k, v := range stream.Metric {
				result.Metric[string(k)] = string(v)
			}

			// parse time series values
			for _, pair := range stream.Values {
				result.Values = append(result.Values, TimeValue{
					Timestamp: pair.Timestamp.Time(),
					Value:     float64(pair.Value),
				})
			}

			results = append(results, result)
		}

	case model.Vector:
		// prometheus vector query result
		for _, sample := range v {
			result := QueryResult{
				Metric: make(map[string]string),
				Values: []TimeValue{
					{
						Timestamp: sample.Timestamp.Time(),
						Value:     float64(sample.Value),
					},
				},
			}

			// parse metric labels
			for k, v := range sample.Metric {
				result.Metric[string(k)] = string(v)
			}
			results = append(results, result)
		}

	case *model.Scalar:
		// prometheus scalar query result
		results = append(results, QueryResult{
			Metric: make(map[string]string),
			Values: []TimeValue{
				{
					Timestamp: v.Timestamp.Time(),
					Value:     float64(v.Value),
				},
			},
		})
	}

	return results
}
