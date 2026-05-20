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
	runModel6Path     = "/v1/run-model6"
	model6StatusPath  = "/v1/model6-status/%s"
	model6ResultsPath = "/v1/model6-results"
	model6CSVPath     = "/v1/model6-csv"
)

func (c *Client) RunAlgorithm6(ctx context.Context) (*ModelRunningResponse, error) {
	req, err := c.newRequest(ctx, http.MethodPost, runModel6Path, nil, "")
	if err != nil {
		return nil, err
	}

	var resp ModelRunningResponse
	return &resp, c.do(req, &resp)

}

func (c *Client) RunAlgorithm6Status(ctx context.Context, taskID string) (*ModelStatusResponse, error) {
	path := fmt.Sprintf(model6StatusPath, url.PathEscape(taskID))
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}

	var resp ModelStatusResponse
	return &resp, c.do(req, &resp)
}

func (c *Client) Algorithm6Result(ctx context.Context, taskID string, w io.Writer) error {
	path := model6ResultsPath
	if taskID != "" {
		path += "?task_id=" + url.QueryEscape(taskID)
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	return c.doDownload(req, w)
}

func (c *Client) Algorithm6CSV(ctx context.Context, taskID string, w io.Writer) error {
	path := model6CSVPath
	if taskID != "" {
		path += "?task_id=" + url.QueryEscape(taskID)
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	return c.doDownload(req, w)
}
