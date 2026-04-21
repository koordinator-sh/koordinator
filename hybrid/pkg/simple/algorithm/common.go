/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-03 @author yangwanjin
 */

package algorithm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"

	"k8s.io/klog/v2"
)

const (
	uploadFilePath   = "/v1/upload-file?extract_to=%s&cleanup_archive=%s"
	uploadStatusPath = "/v1/upload-status/%s"
	cleanPath        = "/v1/clean-directories?clean_data=%s&clean_output=%s"
)

func (c *Client) UploadFile(ctx context.Context, file io.Reader, fileName string, extract ModelType, cleanup bool) (*UploadResponse, error) {
	body, ct, err := buildMultipart("file", fileName, file)
	if err != nil {
		return nil, fmt.Errorf("failed to build multipart: %s", err)
	}

	clean := "true"
	if !cleanup {
		clean = "false"
	}

	extractEncoded := url.QueryEscape(string(extract))
	path := fmt.Sprintf(uploadFilePath, extractEncoded, clean)
	req, err := c.newRequest(ctx, http.MethodPost, path, body, ct)
	if err != nil {
		return nil, err
	}

	var resp UploadResponse
	return &resp, c.do(req, &resp)

}

func buildMultipart(fieldName, filename string, fileContent io.Reader) (*bytes.Buffer, string, error) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, filename))

	part, err := w.CreatePart(h)
	if err != nil {
		return nil, "", fmt.Errorf("create part: %w", err)
	}

	// 复制文件内容到 multipart part
	written, err := io.Copy(part, fileContent)
	if err != nil {
		return nil, "", fmt.Errorf("copy content: %w", err)
	}

	klog.V(5).InfoS("Copied file content to multipart", "filename", filename, "bytes", written)

	if err = w.Close(); err != nil {
		return nil, "", fmt.Errorf("close writer: %w", err)
	}

	return buf, w.FormDataContentType(), nil
}

func (c *Client) UploadStatus(ctx context.Context, taskID string) (*UploadStatusResponse, error) {
	path := fmt.Sprintf(uploadStatusPath, url.PathEscape(taskID))
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}

	var resp UploadStatusResponse
	return &resp, c.do(req, &resp)

}

func (c *Client) CleanDirectories(ctx context.Context, data, output ModelType) (*CleanResponse, error) {
	path := fmt.Sprintf(cleanPath, data, output)
	req, err := c.newRequest(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return nil, err
	}

	var resp CleanResponse
	return &resp, c.do(req, &resp)
}
