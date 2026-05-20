/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-09 @author yangwanjin
 */

package predictor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/klog/v2"

	"hybrid/pkg/constants"
	"hybrid/pkg/simple/algorithm"
)

// DownloadService downloads the prediction CSV from the AI server.
// It implements the Downloader interface.
type DownloadService struct {
	client    *algorithm.Client
	outputDir string
}

// NewDownloadService creates a Service with the given server URL, auth token, and local output directory.
func NewDownloadService(client *algorithm.Client, outputDir string) *DownloadService {
	if outputDir == "" {
		outputDir = constants.DefaultOutputDir
	}
	return &DownloadService{
		outputDir: outputDir,
		client:    client,
	}
}

// Download 下载模型最新的训练结果,依次写入到本地文件中
// Model4: classify-result.csv       (应用分类, ClassifyController 读取)
// Model5: replica-short-result.csv  (短期副本预测, ReplicasController 读取)
// Model5: replica-long-result.csv   (长期副本预测, ReplicasController 读取)
// Model6: interference-result.csv   (干扰分析, InterferenceController 读取)
func (s *DownloadService) Download(ctx context.Context) error {
	if err := os.MkdirAll(s.outputDir, 0o755); err != nil {
		return fmt.Errorf("ensure output dir %q: %w", s.outputDir, err)
	}

	var errs []error

	// 1. 下载 MODEL4 应用分类结果
	if err := s.DownloadModel4(ctx); err != nil {
		klog.ErrorS(err, "Failed to download model4 result")
		errs = append(errs, fmt.Errorf("model4: %w", err))
	}

	// 2. 下载 MODEL5 短期 + 长期副本预测结果
	if err := s.DownloadModel5(ctx); err != nil {
		klog.ErrorS(err, "Failed to download model5 result")
		errs = append(errs, fmt.Errorf("model5: %w", err))
	}

	// 3. 下载 MODEL6 干扰分析结果
	if err := s.DownloadModel6(ctx); err != nil {
		klog.ErrorS(err, "Failed to download model6 result")
		errs = append(errs, fmt.Errorf("model6: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("download partial failure: %v", errs)
	}
	return nil
}

func (s *DownloadService) DownloadModel4(ctx context.Context) error {
	dest := filepath.Join(s.outputDir, constants.DefaultPredictionFile)
	return s.atomicDownload(ctx, dest, func(ctx context.Context, f *os.File) error {
		return s.client.Algorithm4CSV(ctx, "", f)
	})
}

func (s *DownloadService) DownloadModel5(ctx context.Context) error {
	shortDest := filepath.Join(s.outputDir, constants.DefaultReplicaShortFile)
	if err := s.atomicDownload(ctx, shortDest, func(ctx context.Context, f *os.File) error {
		return s.client.Algorithm5ShortCSV(ctx, "", f)
	}); err != nil {
		return fmt.Errorf("short result: %w", err)
	}

	longDest := filepath.Join(s.outputDir, constants.DefaultReplicaLongFile)
	if err := s.atomicDownload(ctx, longDest, func(ctx context.Context, f *os.File) error {
		return s.client.Algorithm5LongCSV(ctx, "", f)
	}); err != nil {
		return fmt.Errorf("long result: %w", err)
	}

	return nil
}

func (s *DownloadService) DownloadModel6(ctx context.Context) error {
	dest := filepath.Join(s.outputDir, constants.DefaultInterferenceFile)
	return s.atomicDownload(ctx, dest, func(ctx context.Context, f *os.File) error {
		return s.client.Algorithm6CSV(ctx, "", f)
	})
}

// atomicDownload 将 fn 的输出写入临时文件,成功后原子 rename 到 dest
// 保证 dest 始终是完整文件,不会暴露写入中途的截断状态
func (s *DownloadService) atomicDownload(ctx context.Context, dest string, fn func(context.Context, *os.File) error) error {
	tmp, err := os.CreateTemp(s.outputDir, ".dl-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// 无论成功与否都清理临时文件(rename 成功后 Remove 是 no-op)
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	if err := fn(ctx, tmp); err != nil {
		return fmt.Errorf("download to temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("flush temp file: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("atomic rename %q to %q: %w", tmpName, dest, err)
	}

	klog.V(4).InfoS("Download algorithm result complete", "dest", dest)
	return nil
}
