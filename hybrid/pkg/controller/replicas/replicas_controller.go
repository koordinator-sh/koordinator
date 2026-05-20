/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-03 @author yangwanjin
 */

// Package replicas implements the ReplicasController, which periodically
// downloads AI replica prediction results (MODEL5 short & long term)
// and writes them as annotations onto the corresponding Kubernetes workloads.
package replicas

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"hybrid/pkg/controller"
	"hybrid/pkg/predictor"
	"hybrid/pkg/simple/algorithm"
	"hybrid/pkg/utils"
)

const controllerName = "replicas-controller"

// Controller syncs AI replica prediction results (short-term and long-term)
// onto Kubernetes workload annotations.
// Short-term (Model5Short) and long-term (Model5Long) tasks are monitored
// independently: each triggers only its own sync when it completes.
type Controller struct {
	patchers          controller.WorkloadPatcherRegistry
	downloader        predictor.Downloader
	fetcher           predictor.Fetcher
	notifyShort       <-chan algorithm.TaskEvent // 订阅 Model5Short 运行成功事件
	notifyLong        <-chan algorithm.TaskEvent // 订阅 Model5Long 运行成功事件
	excludeNamespaces sets.Set[string]
	outputDir         string
}

// Name implements controller.Controller.
func (c *Controller) Name() string { return controllerName }

// Start implements controller.Controller.
// Dependencies are injected from the shared Manager at runtime.
func (c *Controller) Start(ctx context.Context, mgr *controller.Manager) error {
	c.patchers = controller.NewWorkloadPatcherRegistry(mgr.Client)
	c.downloader = mgr.DownloadService
	c.fetcher = mgr.FetchService
	c.notifyShort = mgr.Notifier.Subscribe(algorithm.Model5Short)
	c.notifyLong = mgr.Notifier.Subscribe(algorithm.Model5Long)
	c.excludeNamespaces = mgr.ExcludeNamespaces
	c.outputDir = mgr.OutputDir

	klog.InfoS("ReplicasController starting", "excludeNamespaces", sets.List(c.excludeNamespaces))

	for {
		select {
		case <-ctx.Done():
			klog.Info("ReplicasController stopping")
			return nil
		case event, ok := <-c.notifyShort:
			if !ok {
				return nil
			}
			klog.InfoS("ReplicasController received model5 short task completion", "taskID", event.TaskID)
			if err := c.syncShort(ctx, event.TaskID); err != nil {
				klog.ErrorS(err, "ReplicasController short sync failed", "taskID", event.TaskID)
			}
		case event, ok := <-c.notifyLong:
			if !ok {
				return nil
			}
			klog.InfoS("ReplicasController received model5 long task completion", "taskID", event.TaskID)
			if err := c.syncLong(ctx, event.TaskID); err != nil {
				klog.ErrorS(err, "ReplicasController long sync failed", "taskID", event.TaskID)
			}
		}
	}
}

// syncShort fetches short-term predictions and applies them to workload annotations.
func (c *Controller) syncShort(ctx context.Context, taskID string) error {
	start := time.Now()

	records, err := c.fetcher.FetchModel5ShortResults(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to fetch model5 short results: %w", err)
	}

	count, errCount := c.applyShortTermPredictionsFromRecords(ctx, records)

	klog.InfoS("Short-term replica prediction sync complete",
		"total", count, "errors", errCount, "elapsed", time.Since(start))

	return nil
}

// syncLong fetches long-term predictions and applies them to workload annotations.
func (c *Controller) syncLong(ctx context.Context, taskID string) error {
	start := time.Now()

	records, err := c.fetcher.FetchModel5LongResults(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to fetch model5 long results: %w", err)
	}

	count, errCount := c.applyLongTermPredictionsFromRecords(ctx, records)

	klog.InfoS("Long-term replica prediction sync complete",
		"total", count, "errors", errCount, "elapsed", time.Since(start))

	return nil
}

// applyShortTermPredictionsFromRecords applies short-term replica predictions from fetched records.
func (c *Controller) applyShortTermPredictionsFromRecords(ctx context.Context, records []predictor.ReplicasShortRecord) (int, int) {
	var (
		skipped  int
		errCount int
	)

	for _, rec := range records {
		// Skip excluded namespaces
		if c.excludeNamespaces.Has(rec.Namespace) {
			klog.V(5).InfoS("Skipping excluded namespace", "namespace", rec.Namespace, "pod", rec.Name)
			skipped++
			continue
		}

		replicaCount, _ := strconv.Atoi(rec.ReplicaCount)

		if err := c.applyReplicaAnnotation(ctx, rec.Record, replicaCount, predictor.AnnotationReplicaShort); err != nil {
			klog.ErrorS(err, "Failed to annotate workload with short-term replicas",
				"namespace", rec.Namespace, "workload", rec.Name, "replicas", rec.ReplicaCount)
			errCount++
		}
	}

	klog.V(4).InfoS("Short-term predictions applied", "total", len(records), "skipped", skipped, "errors", errCount)

	return len(records), errCount
}

// applyLongTermPredictionsFromRecords applies long-term replica predictions from fetched records.
func (c *Controller) applyLongTermPredictionsFromRecords(ctx context.Context, records []predictor.ReplicasLongRecord) (int, int) {
	var (
		skipped  int
		errCount int
	)

	for _, rec := range records {
		// Skip excluded namespaces
		if c.excludeNamespaces.Has(rec.Namespace) {
			klog.V(5).InfoS("Skipping excluded namespace", "namespace", rec.Namespace, "pod", rec.Name)
			skipped++
			continue
		}

		recommendReplicas, _ := strconv.Atoi(rec.RecommendReplicas)

		if err := c.applyReplicaAnnotation(ctx, rec.Record, recommendReplicas, predictor.AnnotationReplicaLong); err != nil {
			klog.ErrorS(err, "Failed to annotate workload with long-term replicas",
				"namespace", rec.Namespace, "workload", rec.Name, "replicas", rec.RecommendReplicas)
			errCount++
		}
	}

	klog.V(4).InfoS("Long-term predictions applied",
		"total", len(records), "skipped", skipped, "errors", errCount)

	return len(records), errCount
}

// applyReplicaAnnotation resolves the owning workload for a pod record and patches its annotations.
func (c *Controller) applyReplicaAnnotation(ctx context.Context, rec predictor.Record, replicas int, annotationKey string) error {
	ref, err := utils.GetWorkloadKindByName(ctx, c.patchers.Client(), rec.Namespace, rec.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("Pod not found, skipping", "namespace", rec.Namespace, "name", rec.Name)
			return nil
		}
		return fmt.Errorf("failed to find pod controller: %w", err)
	}

	patcher, ok := c.patchers.Get(ref.Kind)
	if !ok {
		return fmt.Errorf("unsupported workload kind %q (%s/%s)", ref.Kind, rec.Namespace, ref.Name)
	}

	ann := map[string]string{
		annotationKey:                 fmt.Sprintf("%d", replicas),
		predictor.AnnotationTimestamp: time.Now().Format(time.RFC3339),
	}

	if err := patcher.Patch(ctx, rec.Namespace, ref.Name, ann); err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("Workload gone, skipping", "kind", ref.Kind, "namespace", rec.Namespace, "name", ref.Name)
			return nil
		}
		return err
	}

	klog.V(4).InfoS("Replica annotation applied",
		"kind", ref.Kind, "ns", rec.Namespace, "name", ref.Name,
		"annotation", annotationKey, "replicas", replicas)
	return nil
}
