/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-03 @author yangwanjin
 */

// Package classify implements the ClassifyController, which periodically
// downloads AI workload-type predictions and writes them as annotations
// onto the corresponding Kubernetes workloads.
package classify

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"hybrid/pkg/constants"
	"hybrid/pkg/controller"
	"hybrid/pkg/predictor"
	"hybrid/pkg/simple/algorithm"
	"hybrid/pkg/utils"
)

const controllerName = "classify-controller"

// Controller syncs AI classification results onto Kubernetes workload annotations.
// It is registered into the global controller.Controllers registry via server.go init().
type Controller struct {
	patchers          controller.WorkloadPatcherRegistry
	downloader        predictor.Downloader
	fetcher           predictor.Fetcher
	notify            <-chan algorithm.TaskEvent // 订阅模型运行成功事件
	excludeNamespaces sets.Set[string]
	predFile          string
}

// Name implements controller.Controller.
func (c *Controller) Name() string { return controllerName }

// Start implements controller.Controller.
// Dependencies are injected from the shared Manager at runtime.
func (c *Controller) Start(ctx context.Context, mgr *controller.Manager) error {
	c.patchers = controller.NewWorkloadPatcherRegistry(mgr.Client)
	c.downloader = mgr.DownloadService
	c.fetcher = mgr.FetchService
	c.notify = mgr.Notifier.Subscribe(algorithm.Model4)
	c.excludeNamespaces = mgr.ExcludeNamespaces
	c.predFile = filepath.Join(mgr.OutputDir, constants.DefaultPredictionFile)

	klog.InfoS("ClassifyController starting", "excludeNamespaces", sets.List(c.excludeNamespaces))

	for {
		select {
		case <-ctx.Done():
			klog.Info("ClassifyController stopping")
			return nil
		case event, ok := <-c.notify:
			if !ok {
				return nil
			}
			klog.InfoS("ClassifyController received model4 task completion", "taskID", event.TaskID)
			if err := c.sync(ctx, event.TaskID); err != nil {
				klog.ErrorS(err, "ClassifyController sync failed", "taskID", event.TaskID)
			}
		}
	}
}

// sync executes one full download → parse → annotate cycle.
func (c *Controller) sync(ctx context.Context, taskID string) error {
	start := time.Now()

	// 获取 MODEL4 的结果
	records, err := c.fetcher.FetchModel4Results(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to parse: %w", err)
	}

	var (
		skipped  int
		errCount int
	)

	for _, rec := range records {
		// Skip namespaces the operator has opted out of.
		if c.excludeNamespaces.Has(rec.Namespace) {
			klog.V(5).InfoS("Skipping excluded namespace", "namespace", rec.Namespace, "pod", rec.Name)
			skipped++
			continue
		}

		if err := c.applyAnnotations(ctx, rec); err != nil {
			klog.ErrorS(err, "Failed to annotate workload", "namespace", rec.Namespace, "pod", rec.Name)
			errCount++
		}
	}

	klog.InfoS("Classification sync complete",
		"total", len(records), "skipped", skipped, "errors", errCount, "elapsed", time.Since(start))

	return nil
}

// applyAnnotations resolves the owning workload for a pod record and patches its annotations.
func (c *Controller) applyAnnotations(ctx context.Context, rec predictor.PodRecord) error {
	ref, err := utils.GetControllerInfoForPod(ctx, c.patchers.Client(), rec.Namespace, rec.Name)
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
		predictor.AnnotationPredictedType: rec.PredictedType,
		predictor.AnnotationTimestamp:     time.Now().Format(time.RFC3339),
	}

	if err := patcher.Patch(ctx, rec.Namespace, ref.Name, ann); err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("Workload gone, skipping", "kind", ref.Kind, "namespace", rec.Namespace, "name", ref.Name)
			return nil
		}
		return err
	}

	klog.V(4).InfoS("Annotation applied", "kind", ref.Kind, "ns", rec.Namespace, "name", ref.Name, "type", rec.PredictedType)
	return nil
}
