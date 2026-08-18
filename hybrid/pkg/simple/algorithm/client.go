/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-03 @author yangwanjin
 */

package algorithm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Signal struct {
	Model    string `json:"model"`
	Ready    bool   `json:"ready"`
	UploadID string `json:"upload_id"`
}

type Option func(*Client)

type Client struct {
	url        string
	token      string
	httpClient *http.Client
}

func WithURL(url string) Option {
	return func(c *Client) {
		c.url = url
	}
}

func WithToken(token string) Option {
	return func(c *Client) {
		c.token = token
	}
}

func NewClient(opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Request, error) {
	parse, err := url.Parse(c.url + path)
	if err != nil {
		return nil, fmt.Errorf("invalid parse: %s", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, parse.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build request: %s", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-token", c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return req, nil
}

func (c *Client) newJSONRequest(ctx context.Context, path string, payload interface{}) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body), "application/json")
}

func (c *Client) do(req *http.Request, v interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, raw)
	}

	if v == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(v)

}

func (c *Client) doDownload(req *http.Request, w io.Writer) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, raw)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}
