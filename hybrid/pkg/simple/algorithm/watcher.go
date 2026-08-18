/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-09 @author yangwanjin
 */

package algorithm

import (
	"context"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

const (
	defaultPollInterval     = 10 * time.Second  // 短期/其他任务轮询间隔
	defaultPollTimeout      = 120 * time.Minute // 短期/其他任务最长等待时间
	defaultLongPollInterval = 15 * time.Minute  // 长期任务轮询间隔（Model5Long）
	defaultLongPollTimeout  = 30 * time.Hour    // 长期任务最长等待时间（>24h）
)

// WatcherOptions 控制轮询行为的可配置参数。
// 短期/其他模型使用 PollInterval/PollTimeout；
// Model5Long 使用 LongPollInterval/LongPollTimeout。
type WatcherOptions struct {
	PollInterval     time.Duration
	PollTimeout      time.Duration
	LongPollInterval time.Duration
	LongPollTimeout  time.Duration
}

func (o *WatcherOptions) applyDefaults() {
	if o.PollInterval <= 0 {
		o.PollInterval = defaultPollInterval
	}
	if o.PollTimeout <= 0 {
		o.PollTimeout = defaultPollTimeout
	}
	if o.LongPollInterval <= 0 {
		o.LongPollInterval = defaultLongPollInterval
	}
	if o.LongPollTimeout <= 0 {
		o.LongPollTimeout = defaultLongPollTimeout
	}
}

// Watcher 异步轮询每个模型算法的任务状态,任务运行成功后通过 Notifier 推送 TaskEvent 事件到对应控制器立即触发 sync
// 同一个 ModelType 同一时刻只允许运行一个轮询协程,重复提交会直接跳过
type Watcher struct {
	client   *Client
	notifier *Notifier // 任务运行成功后推送事件
	opts     WatcherOptions
	mu       sync.Mutex
	running  map[ModelType]bool // 正在运行的模型
}

func NewWatcher(client *Client, notifier *Notifier, opts WatcherOptions) *Watcher {
	opts.applyDefaults()
	return &Watcher{
		client:   client,
		notifier: notifier,
		opts:     opts,
		running:  make(map[ModelType]bool),
	}
}

// IsRunning 返回指定模型当前是否有轮询协程正在运行（即上一个任务尚未完成）。
// 用于在启动新任务前判断是否应该跳过 RunAlgorithm 调用。
func (w *Watcher) IsRunning(model ModelType) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running[model]
}

// Watch 指定 MODEL 的 TaskID 启动异步状态轮询,同一 MODEL 若已有轮询正在运行,则跳过本次(上一个任务未完成时不重复监听)
func (w *Watcher) Watch(ctx context.Context, model ModelType, taskID string) {
	w.mu.Lock()
	if w.running[model] {
		w.mu.Unlock()
		klog.InfoS("Watcher already polling for model, skipping new task", "model", model, "newTaskID", taskID)
		return
	}
	w.running[model] = true
	w.mu.Unlock()

	go func() {
		defer func() {
			w.mu.Lock()
			w.running[model] = false
			w.mu.Unlock()
		}()

		klog.InfoS("Watcher started", "model", model, "taskID", taskID)
		w.poll(ctx, model, taskID)
	}()
}

// poll 持续轮询直到任务完成、超时、ctx 被取消
func (w *Watcher) poll(ctx context.Context, model ModelType, taskID string) {
	interval, timeout := w.opts.PollInterval, w.opts.PollTimeout
	if model == Model5Long {
		interval, timeout = w.opts.LongPollInterval, w.opts.LongPollTimeout
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			if ctx.Err() != nil {
				// 父 context 取消(程序正常关闭),静默退出
				klog.V(4).InfoS("Watcher context cancelled", "model", model, "taskID", taskID)
			} else {
				// 算法自身发生超时
				klog.ErrorS(nil, "Watcher timed out waiting for task", "model", model, "taskID", taskID, "timeout", timeout)
			}
			return

		case <-ticker.C:
			status, err := w.queryStatus(timeoutCtx, model, taskID)
			if err != nil {
				// 网络抖动不立即放弃,等下一个 tick 重试
				klog.ErrorS(err, "Watcher query status failed, will retry", "model", model, "taskID", taskID)
				continue
			}

			klog.V(5).InfoS("Watcher polled task", "model", model, "taskID", taskID, "status", status)

			switch status {
			case StatusSuccess:
				klog.InfoS("Watcher running task succeeded, notifying controller", "model", model, "taskID", taskID)
				// 算法运行成功,通知控制器负责更新
				w.notifier.notify(TaskEvent{Model: model, TaskID: taskID})
				return
			case StatusFailure:
				klog.ErrorS(nil, "Watcher running task failed on server side", "model", model, "taskID", taskID, "status", status)
				return
			case StatusDone:
				klog.InfoS("Watcher unknown model type", "model", model, "taskID", taskID)
				return
			default:
				// PROGRESS 继续等待
				klog.InfoS("Algorithm still in progress", "model", model, "taskID", taskID, "status", status)
			}
		}
	}
}

// queryStatus 调用对应模型的查询状态的接口,返回状态字符串
func (w *Watcher) queryStatus(ctx context.Context, model ModelType, taskID string) (string, error) {
	switch model {
	case Model4:
		resp, err := w.client.RunAlgorithm4Status(ctx, taskID)
		if err != nil {
			return "", err
		}
		return processStatus(resp)
	case Model5, Model5Short, Model5Long:
		resp, err := w.client.RunAlgorithm5Status(ctx, taskID)
		if err != nil {
			return "", err
		}
		return processStatus(resp)
	case Model6:
		resp, err := w.client.RunAlgorithm6Status(ctx, taskID)
		if err != nil {
			return "", err
		}
		return processStatus(resp)
	default:
		return StatusDone, nil // 未知 MODEL 直接结束, 不阻塞
	}
}

func processStatus(resp *ModelStatusResponse) (string, error) {
	if resp.Status == StatusSuccess {
		// The API uses the top-level status as the authoritative task state.
		// The nested result payload differs between models and does not always
		// contain a status field.
		return StatusSuccess, nil
	}
	if resp.Status == StatusFailure {
		return StatusFailure, nil
	}
	if resp.Status == StatusRetry {
		return StatusRetry, nil
	}
	return StatusProgress, nil
}
