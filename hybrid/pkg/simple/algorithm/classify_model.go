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
	runModel4Path     = "/v1/run-model4"
	model4StatusPath  = "/v1/model4-status/%s"
	model4ResultsPath = "/v1/model4-results"
	model4CSVPath     = "/v1/model4-csv"
)

func (c *Client) RunAlgorithm4(ctx context.Context, uploadID string) (*ModelRunningResponse, error) {
	req, err := c.newJSONRequest(ctx, runModel4Path, struct {
		UploadID string `json:"upload_id"`
	}{UploadID: uploadID})
	if err != nil {
		return nil, err
	}

	var resp ModelRunningResponse
	return &resp, c.do(req, &resp)

}

func (c *Client) RunAlgorithm4Status(ctx context.Context, taskID string) (*ModelStatusResponse, error) {
	path := fmt.Sprintf(model4StatusPath, url.PathEscape(taskID))
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}

	var resp ModelStatusResponse
	return &resp, c.do(req, &resp)
}

func (c *Client) Algorithm4Result(ctx context.Context, taskID string, w io.Writer) error {
	path := model4ResultsPath
	if taskID != "" {
		path += "?task_id=" + url.QueryEscape(taskID)
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	return c.doDownload(req, w)
}

func (c *Client) Algorithm4CSV(ctx context.Context, taskID string, w io.Writer) error {
	path := model4CSVPath
	if taskID != "" {
		path += "?task_id=" + url.QueryEscape(taskID)
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	return c.doDownload(req, w)
}
