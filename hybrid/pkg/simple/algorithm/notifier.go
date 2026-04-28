/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-09 @author yangwanjin
 */

package algorithm

import (
	"sync"

	"k8s.io/klog/v2"
)

// TaskEvent 用于Watcher 检测到算法运行成功后向对应的控制器发出通知
type TaskEvent struct {
	Model  ModelType // 任务对应的模型
	TaskID string    // 任务的 ID
}

// Notifier 负载将算法任务完成事件按 ModelType 路由给各控制器
//
//	notifier := algorithm.NewNotifier()
//	ch4 := notifier.Subscribe(algorithm.Model4)   // ClassifyController 订阅 (MODEL4)
//	ch5 := notifier.Subscribe(algorithm.Model5)   // ReplicasController 订阅 (MODEL5)
//	ch6 := notifier.Subscribe(algorithm.Model6)   // InterferenceController 订阅 (MODEL6)
//
//	// Watcher 内部在任务成功时调用
//	e.g. notifier.notify(TaskEvent{Model: Model4, TaskID: "xxx"}) 将成功的 TaskEvent 发送给订阅的模型四的 channel
type Notifier struct {
	mu   sync.RWMutex
	subs map[ModelType][]chan TaskEvent
}

func NewNotifier() *Notifier {
	return &Notifier{
		subs: make(map[ModelType][]chan TaskEvent),
	}
}

// Subscribe 会为指定 MODEL 注册一个订阅 channel, 返回只读端
// channel 容量为 1 控制器处理上一个事件期间最多缓冲一个新事件, 不阻塞 Notifier
// 在 notify 首次调用之前完成所有 Subscribe, 通常在进程启动时一次性完成
func (n *Notifier) Subscribe(model ModelType) <-chan TaskEvent {
	ch := make(chan TaskEvent, 1)
	n.mu.Lock()
	n.subs[model] = append(n.subs[model], ch)
	n.mu.Unlock()
	return ch
}

// notify 将 TaskEvent 推送给所有订阅了指定 MODEL 的 channel
// 非阻塞: channel 满时打 Warning 日志, 不会堵塞调用方(Watcher goroutine)
func (n *Notifier) notify(event TaskEvent) {
	n.mu.RLock()
	subs := n.subs[event.Model]
	n.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			klog.Warningf("Notify: subscriber channel full, event dropped, MODEL: %s ,TaskID: %s", event.Model, event.TaskID)
		}
	}
}
