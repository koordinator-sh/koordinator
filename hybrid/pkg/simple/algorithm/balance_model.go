/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-03 @author yangwanjin
 */

package algorithm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	runModel5ShortPath    = "/v1/run-model5-short"
	runModel5LongPath     = "/v1/run-model5-long"
	model5StatusPath      = "/v1/model5-status/%s"
	model5ShortResultPath = "/v1/model5-short-results"
	model5LongResultPath  = "/v1/model5-long-results"
	model5ShortCSVPath    = "/v1/model5-short-csv"
	model5LongCSVPath     = "/v1/model5-long-csv"
)

func (c *Client) RunAlgorithm5Short(ctx context.Context) (*ModelRunningResponse, error) {
	req, err := c.newRequest(ctx, http.MethodPost, runModel5ShortPath, nil, "")
	if err != nil {
		return nil, err
	}

	var resp ModelRunningResponse
	return &resp, c.do(req, &resp)

}
func (c *Client) RunAlgorithm5Long(ctx context.Context) (*ModelRunningResponse, error) {
	req, err := c.newRequest(ctx, http.MethodPost, runModel5LongPath, nil, "")
	if err != nil {
		return nil, err
	}

	var resp ModelRunningResponse
	return &resp, c.do(req, &resp)

}

func (c *Client) RunAlgorithm5Status(ctx context.Context, taskID string) (*ModelStatusResponse, error) {
	path := fmt.Sprintf(model5StatusPath, url.PathEscape(taskID))
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}

	var resp ModelStatusResponse
	return &resp, c.do(req, &resp)
}

func (c *Client) Algorithm5ShortResult(ctx context.Context, taskID string, w io.Writer) error {
	path := model5ShortResultPath
	if taskID != "" {
		path += "?task_id=" + url.QueryEscape(taskID)
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	return c.doDownload(req, w)
}

func (c *Client) Algorithm5LongResult(ctx context.Context, taskID string, w io.Writer) error {
	path := model5LongResultPath
	if taskID != "" {
		path += "?task_id=" + url.QueryEscape(taskID)
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	return c.doDownload(req, w)
}

func (c *Client) Algorithm5ShortCSV(ctx context.Context, taskID string, w io.Writer) error {
	path := model5ShortCSVPath
	if taskID != "" {
		path += "?task_id=" + url.QueryEscape(taskID)
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	return c.doDownload(req, w)
}

func (c *Client) Algorithm5LongCSV(ctx context.Context, taskID string, w io.Writer) error {
	path := model5LongCSVPath
	if taskID != "" {
		path += "?task_id=" + url.QueryEscape(taskID)
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	return c.doDownload(req, w)
}
