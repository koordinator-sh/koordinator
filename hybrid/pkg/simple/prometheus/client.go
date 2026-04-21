/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-01-30 @author yangwanjin
 */

package prometheus

import (
	"context"
	"fmt"
	"net/http"
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

type basicAuthRoundTripper struct {
	username string
	password string
	next     http.RoundTripper
}

func (b *basicAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.SetBasicAuth(b.username, b.password)
	return b.next.RoundTrip(req)
}

type bearerTokenRoundTripper struct {
	token string
	next  http.RoundTripper
}

func (b *bearerTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(req)
}

func setupAuth(auth collector.AuthConfig, transport http.RoundTripper) (http.RoundTripper, error) {
	if transport == nil {
		transport = http.DefaultTransport
	}
	switch auth.Type {
	case "basic":
		if auth.Username == "" || auth.Password == "" {
			klog.Errorf("Basic auth enabled but username/password is empty")
			return nil, fmt.Errorf("basic auth enabled but username/password is empty")
		}
		return &basicAuthRoundTripper{
			username: auth.Username,
			password: auth.Password,
			next:     transport,
		}, nil
	case "bearer":
		if auth.Token == "" {
			klog.Errorf("Bearer token auth enabled but token is empty")
			return nil, fmt.Errorf("bearer auth enabled but token is empty")
		}
		return &bearerTokenRoundTripper{
			token: auth.Token,
			next:  transport,
		}, nil
	case "none", "":
		return transport, nil
	default:
		klog.Infof("Unknown auth type: %s, using no authentication", auth.Type)
		return transport, nil
	}
}

// NewClient create prometheus client
func NewClient(cfg collector.PrometheusConfig) (*Client, error) {
	// create http client
	httpClient := &http.Client{
		Timeout: cfg.Timeout,
	}

	// TODO: skip tls

	roundTripper, err := setupAuth(cfg.Auth, httpClient.Transport)
	if err != nil {
		return nil, err
	}

	client, err := api.NewClient(api.Config{
		Address:      cfg.URL,
		RoundTripper: roundTripper,
	})
	if err != nil {
		return nil, err
	}

	klog.Infof("Successfully created prometheus client, url: %s, auth: %s", cfg.URL, cfg.Auth.Type)
	return &Client{
		api:    v1.NewAPI(client),
		config: cfg,
	}, nil
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
