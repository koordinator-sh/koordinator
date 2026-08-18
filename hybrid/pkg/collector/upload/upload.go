/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-10 @author yangwanjin
 */

package upload

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"hybrid/pkg/simple/algorithm"
)

const (
	cleanupInterval = 8 * time.Hour // 1天
)

type Service struct {
	client  *algorithm.Client
	watcher *algorithm.Watcher // watcher 监听算法运行的状态,成功之后发送信号
	mgrCtx  context.Context    // Manager 生命周期 ctx, 绑定 watcher goroutine
	mu      sync.Mutex
	beginCh chan string
	stopCh  chan struct{}
	readyCh chan algorithm.Signal
}

func NewService(client *algorithm.Client, watcher *algorithm.Watcher, mgrCtx context.Context) *Service {
	return &Service{
		client:  client,
		watcher: watcher,
		mgrCtx:  mgrCtx,
		beginCh: make(chan string, 1),
		readyCh: make(chan algorithm.Signal, len(algorithm.Models)),
		stopCh:  make(chan struct{}),
	}
}

func (s *Service) Run() {
	klog.Info("Upload service starting...")
	go s.worker()
	// 启动定时清理任务,每12h清理一次所有模型数据
	go s.startCleanupTicker()
	klog.Info("Upload service started")
}

func (s *Service) Stop() {
	klog.Info("Upload service stopping...")
	close(s.stopCh)
}

// startCleanupTicker 启动定时清理任务,每24h清理一次所有模型的数据
func (s *Service) startCleanupTicker() {
	// 首次清理在启动后 8 小时执行,避免刚启动就清理
	initialDelay := 4 * time.Hour
	klog.InfoS("Cleanup ticker started", "interval", cleanupInterval, "firstCleanupIn", initialDelay)

	// 等待初始延迟
	select {
	case <-time.After(initialDelay):
		klog.Info("Initial cleanup delay completed, starting first cleanup")
	case <-s.mgrCtx.Done():
		klog.Info("Cleanup ticker stopped before first cleanup (context cancelled)")
		return
	}

	// 执行首次清理
	s.cleanupAllModels()

	// 创建定时器,按固定间隔清理
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.mgrCtx.Done():
			klog.Info("Cleanup ticker stopped (context cancelled)")
			return
		case <-ticker.C:
			klog.Info("Scheduled cleanup triggered")
			s.cleanupAllModels()
		}
	}
}

// cleanupAllModels 清理所有模型的数据和输出文件,避免占用磁盘空间
func (s *Service) cleanupAllModels() {
	klog.Info("Starting cleanup for all models...")

	ctx, cancel := context.WithTimeout(s.mgrCtx, 10*time.Minute)
	defer cancel()

	var successCount, failCount int

	// 遍历所有模型,清理数据和输出文件
	for _, model := range algorithm.Models {
		klog.InfoS("Cleaning up model", "model", model)

		resp, err := s.client.CleanDirectories(ctx, model, model)
		if err != nil {
			klog.ErrorS(err, "Failed to cleanup model", "model", model)
			failCount++
			continue
		}

		if resp.Status == algorithm.StatusSuccess {
			klog.InfoS("Model cleanup succeeded", "model", model, "status", resp.Status, "message", resp.Message)
			successCount++
		} else {
			klog.ErrorS(nil, "Model cleanup failed", "model", model, "status", resp.Status, "message", resp.Message)
			failCount++
		}
	}

	klog.InfoS("Cleanup completed", "totalModels", len(algorithm.Models), "success", successCount, "failed", failCount)
}

func (s *Service) NotifyBegin(path string) {
	select {
	case s.beginCh <- path:
		klog.V(4).Infof("Notified upload service of new data: %s", path)
	default:
		klog.Warningf("Upload notification channel is full, skipping notification for: %s", path)
	}
}

func (s *Service) notifyReady(signal algorithm.Signal) {
	select {
	case s.readyCh <- signal:
		klog.Infof("Upload file ready, notify running for model: %s", signal.Model)
	default:
		klog.Warningf("Notify readyCh has full, skipping notification")
	}
}

func (s *Service) worker() {
	for {
		select {
		// 接收数据收集完成信号,启动数据上传
		case dataPath := <-s.beginCh:
			if err := s.process(dataPath); err != nil {
				klog.Errorf("Failed to process and upload: %v", err)
			}

		// 数据上传成功, 通知触发对应模型训练的任务
		case sig := <-s.readyCh:
			if err := s.triggerAlgorithm(sig); err != nil {
				klog.Errorf("Failed to trigger algorithm for model %s: %v", sig.Model, err)
			}

		case <-s.stopCh:
			klog.Infof("Upload worker stopped")
			return
		}
	}
}

func (s *Service) process(dataPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// upload file
	if err := s.uploadFile(dataPath); err != nil {
		return fmt.Errorf("failed to upload : %w", err)
	}

	return nil
}

func (s *Service) uploadFile(filePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fileName := filepath.Base(filePath)
	var errs []error

	for _, model := range algorithm.Models {
		tempFile, err := s.createTempCopy(filePath, model)
		if err != nil {
			errs = append(errs, fmt.Errorf("model %s: failed to create temp copy: %w", model, err))
			continue
		}
		tempPath := tempFile.Name()
		defer os.Remove(tempPath)

		// 每个 Model 使用唯一的文件名, 避免服务端同名覆盖
		modelFileName := fmt.Sprintf("%s_%s", strings.ToLower(string(model)), fileName)

		if err := s.uploadModelFile(ctx, tempFile, modelFileName, model); err != nil {
			errs = append(errs, fmt.Errorf("model %s: %w", model, err))
			tempFile.Close()
			continue
		}
		tempFile.Close()
	}

	if len(errs) > 0 {
		return fmt.Errorf("upload partial failure: %v", errs)
	}
	return nil
}

// createTempCopy 创建文件的临时副本
func (s *Service) createTempCopy(srcPath string, model algorithm.ModelType) (*os.File, error) {
	// 创建临时文件
	tempDir := filepath.Dir(srcPath)
	tempFile, err := os.CreateTemp(tempDir, fmt.Sprintf("upload-%s-*.gz", model))
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}

	// 打开源文件
	srcFile, err := os.Open(srcPath)
	if err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("open source file: %w", err)
	}
	defer srcFile.Close()

	// 复制内容
	if _, err := io.Copy(tempFile, srcFile); err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("copy file content: %w", err)
	}

	// 重置临时文件指针到开头,准备上传
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("seek temp file: %w", err)
	}

	klog.V(5).InfoS("Created temp copy", "source", srcPath, "temp", tempFile.Name())
	return tempFile, nil
}

func (s *Service) uploadModelFile(ctx context.Context, f io.Reader, fileName string, modelType algorithm.ModelType) error {
	klog.Infof("Uploading metrics data (file: %s) to the algorithm model %s.", fileName, modelType)

	resp, err := s.client.UploadFile(ctx, f, fileName, modelType, true)
	if err != nil {
		return fmt.Errorf("failed to upload: %w", err)
	}

	uploadID := resp.UploadID
	if uploadID == "" {
		uploadID = resp.TaskID
	}
	if uploadID == "" {
		return fmt.Errorf("upload response did not contain upload_id or task_id")
	}

	klog.Infof("Model %s upload accepted, uploadID=%s", modelType, uploadID)

	// 启动异步协程轮询上传状态,文件上传成功后发送模型
	go s.watchUploadStatus(s.mgrCtx, modelType, uploadID)

	return nil
}

// watchUploadStatus 异步轮询上传任务状态,直到上传成功或失败
func (s *Service) watchUploadStatus(ctx context.Context, modelType algorithm.ModelType, uploadID string) {
	const (
		pollInterval = 10 * time.Second // 轮询间隔
		pollTimeout  = 10 * time.Minute // 上传超时时间
	)

	timeoutCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	klog.InfoS("Watching upload status", "model", modelType, "uploadID", uploadID)

	for {
		select {
		case <-timeoutCtx.Done():
			if ctx.Err() != nil {
				klog.V(4).InfoS("Upload watcher context cancelled", "model", modelType, "uploadID", uploadID)
			} else {
				klog.ErrorS(nil, "Upload watcher timed out", "model", modelType, "uploadID", uploadID, "timeout", pollTimeout)
			}
			return

		case <-ticker.C:
			status, err := s.queryUploadStatus(timeoutCtx, uploadID)
			if err != nil {
				klog.ErrorS(err, "Failed to query upload status, will retry", "model", modelType, "uploadID", uploadID)
				continue
			}

			klog.V(5).InfoS("Polled upload status", "model", modelType, "uploadID", uploadID, "status", status)

			switch status {
			case algorithm.StatusSuccess:
				klog.InfoS("Upload succeeded, notifying triggerAlgorithm", "model", modelType, "uploadID", uploadID)
				// 上传成功, 通知触发算法运行
				s.notifyReady(algorithm.Signal{Model: string(modelType), Ready: true, UploadID: uploadID})
				return

			case algorithm.StatusFailure:
				klog.ErrorS(nil, "Upload failed on server side", "model", modelType, "uploadID", uploadID, "status", status)
				return

			default:
				// PENDING / PROGRESS  继续等待
				klog.V(4).InfoS("Upload still in progress", "model", modelType, "uploadID", uploadID, "status", status)
			}
		}
	}
}

// queryUploadStatus 查询上传任务状态
func (s *Service) queryUploadStatus(ctx context.Context, taskID string) (string, error) {
	resp, err := s.client.UploadStatus(ctx, taskID)
	if err != nil {
		return "", err
	}

	// 上传服务以顶层 status 作为任务状态；成功响应的 result 结构不保证包含
	// success 字段，因此不能依赖 resp.Result.Success 判断上传是否成功。
	if resp.Status == algorithm.StatusSuccess {
		return algorithm.StatusSuccess, nil
	}

	if resp.Status == algorithm.StatusFailure {
		return algorithm.StatusFailure, nil
	}

	return algorithm.StatusPending, nil
}

func (s *Service) triggerAlgorithm(sig algorithm.Signal) error {
	// skip if not ready
	if !sig.Ready {
		return nil
	}
	if sig.UploadID == "" {
		return fmt.Errorf("cannot run model %s without upload_id", sig.Model)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	klog.Infof("Running algorithm for model: %s", sig.Model)

	switch algorithm.ModelType(sig.Model) {
	case algorithm.Model4:
		if s.watcher.IsRunning(algorithm.Model4) {
			klog.Infof("model4 previous task still running, skipping trigger")
			return nil
		}
		resp, err := s.client.RunAlgorithm4(ctx, sig.UploadID)
		if err != nil {
			return fmt.Errorf("failed to run model4 algorithm: %w", err)
		}
		klog.Infof("model4 algorithm started, taskID=%s", resp.TaskID)
		s.watcher.Watch(s.mgrCtx, algorithm.Model4, resp.TaskID)

	case algorithm.Model5:
		if s.watcher.IsRunning(algorithm.Model5Short) {
			klog.Infof("model5 short previous task still running, skipping trigger")
		} else {
			respShort, err := s.client.RunAlgorithm5Short(ctx, sig.UploadID)
			if err != nil {
				return fmt.Errorf("failed to run model5 algorithm for short: %w", err)
			}
			klog.Infof("model5 algorithm for short started, taskID=%s", respShort.TaskID)
			s.watcher.Watch(s.mgrCtx, algorithm.Model5Short, respShort.TaskID)
		}

		if s.watcher.IsRunning(algorithm.Model5Long) {
			klog.Infof("model5 long previous task still running, skipping trigger")
		} else {
			respLong, err := s.client.RunAlgorithm5Long(ctx, sig.UploadID)
			if err != nil {
				return fmt.Errorf("failed to run model5 algorithm for long: %w", err)
			}
			klog.Infof("model5 algorithm for long started, taskID=%s", respLong.TaskID)
			s.watcher.Watch(s.mgrCtx, algorithm.Model5Long, respLong.TaskID)
		}

	case algorithm.Model6:
		if s.watcher.IsRunning(algorithm.Model6) {
			klog.Infof("model6 previous task still running, skipping trigger")
			return nil
		}
		resp, err := s.client.RunAlgorithm6(ctx, sig.UploadID)
		if err != nil {
			return fmt.Errorf("failed to run model6 algorithm: %w", err)
		}
		klog.Infof("model6 algorithm started, taskID=%s", resp.TaskID)
		s.watcher.Watch(s.mgrCtx, algorithm.Model6, resp.TaskID)

	default:
		return fmt.Errorf("unknown model type: %s", sig.Model)
	}

	return nil
}
