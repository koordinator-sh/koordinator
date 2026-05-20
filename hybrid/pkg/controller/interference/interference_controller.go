/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-07 @author yangwanjin
 */

// Package interference implements the InterferenceController, which periodically
// downloads AI interference analysis results (MODEL6) and writes them as annotations
// onto the corresponding Kubernetes workloads.
package interference

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"hybrid/pkg/controller"
	"hybrid/pkg/predictor"
	"hybrid/pkg/simple/algorithm"
	"hybrid/pkg/utils"
)

const controllerName = "interference-controller"

// Controller syncs AI interference analysis results onto Kubernetes workload annotations.
type Controller struct {
	patchers          controller.WorkloadPatcherRegistry
	downloader        predictor.Downloader
	fetcher           predictor.Fetcher
	notify            <-chan algorithm.TaskEvent // 订阅 Model6 运行成功事件
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
	c.notify = mgr.Notifier.Subscribe(algorithm.Model6)
	c.excludeNamespaces = mgr.ExcludeNamespaces
	c.outputDir = mgr.OutputDir

	klog.InfoS("InterferenceController starting", "excludeNamespaces", sets.List(c.excludeNamespaces))

	for {
		select {
		case <-ctx.Done():
			klog.Info("InterferenceController stopping")
			return nil
		case event, ok := <-c.notify:
			if !ok {
				return nil
			}
			klog.InfoS("InterferenceController received model6 task completion", "taskID", event.TaskID)
			if err := c.sync(ctx, event.TaskID); err != nil {
				klog.ErrorS(err, "InterferenceController sync failed", "taskID", event.TaskID)
			}
		}
	}
}

// sync executes one full fetch → annotate cycle for interference analysis.
func (c *Controller) sync(ctx context.Context, taskID string) error {
	start := time.Now()

	// Fetch MODEL6 interference analysis results from algorithm service
	records, err := c.fetcher.FetchModel6Results(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to fetch model6 results: %w", err)
	}

	// Apply interference annotations
	count, errCount := c.applyInterferenceAnnotationsFromRecords(ctx, records)

	klog.InfoS("Interference analysis sync complete", "total", count, "errors", errCount, "elapsed", time.Since(start))

	return nil
}

// applyInterferenceAnnotationsFromRecords applies interference annotations from fetched records.
func (c *Controller) applyInterferenceAnnotationsFromRecords(ctx context.Context, records []predictor.InterferenceRecord) (int, int) {
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

		if err := c.applyInterferenceAnnotation(ctx, rec); err != nil {
			klog.ErrorS(err, "Failed to annotate workload with interference info",
				"namespace", rec.Namespace, "pod", rec.Name)
			errCount++
		}
	}

	klog.V(4).InfoS("Interference annotations applied", "total", len(records), "skipped", skipped, "errors", errCount)

	return len(records), errCount
}

// applyInterferenceAnnotation resolves the owning workload for a pod record and patches its annotations.
func (c *Controller) applyInterferenceAnnotation(ctx context.Context, rec predictor.InterferenceRecord) error {
	ref, err := utils.GetControllerInfoForPod(ctx, c.patchers.Client(), rec.Namespace, rec.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("Workload not found, skipping", "namespace", rec.Namespace, "name", rec.Name)
			return nil
		}
		return fmt.Errorf("failed to find workload: %w", err)
	}

	patcher, ok := c.patchers.Get(ref.Kind)
	if !ok {
		return fmt.Errorf("unsupported workload kind %q (%s/%s)", ref.Kind, rec.Namespace, rec.Name)
	}

	// Serialize interference data as JSON for the annotation
	interferenceData := map[string]string{
		"type":   rec.InterferenceType,
		"level":  rec.InterferenceLevel,
		"signal": rec.InterferenceSignal,
		"reason": rec.InterferenceReason,
		"action": rec.RecommendAction,
	}

	interferenceJSON, err := json.Marshal(interferenceData)
	if err != nil {
		return fmt.Errorf("failed to marshal interference data: %w", err)
	}

	ann := map[string]string{
		predictor.AnnotationInterference: string(interferenceJSON),
		predictor.AnnotationTimestamp:    time.Now().Format(time.RFC3339),
	}

	if err := patcher.Patch(ctx, rec.Namespace, ref.Name, ann); err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("Workload gone, skipping", "kind", ref.Kind, "namespace", rec.Namespace, "name", ref.Name)
			return nil
		}
		return err
	}

	klog.V(4).InfoS("Interference annotation applied",
		"kind", ref.Kind, "ns", rec.Namespace, "name", ref.Name,
		"type", rec.InterferenceType, "level", rec.InterferenceLevel,
		"signal", rec.InterferenceSignal, "action", rec.RecommendAction)
	return nil
}
