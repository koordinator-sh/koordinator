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
type Controller struct {
	patchers          controller.WorkloadPatcherRegistry
	downloader        predictor.Downloader
	fetcher           predictor.Fetcher
	notify            <-chan algorithm.TaskEvent // 订阅 Model5 运行成功事件
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
	c.notify = mgr.Notifier.Subscribe(algorithm.Model5)
	c.excludeNamespaces = mgr.ExcludeNamespaces
	c.outputDir = mgr.OutputDir

	klog.InfoS("ReplicasController starting", "excludeNamespaces", sets.List(c.excludeNamespaces))

	for {
		select {
		case <-ctx.Done():
			klog.Info("ReplicasController stopping")
			return nil
		case event, ok := <-c.notify:
			if !ok {
				return nil
			}
			klog.InfoS("ReplicasController received model5 task completion", "taskID", event.TaskID)
			if err := c.sync(ctx); err != nil {
				klog.ErrorS(err, "ReplicasController sync failed", "taskID", event.TaskID)
			}
		}
	}
}

// sync executes one full fetch → annotate cycle for both short and long term predictions.
func (c *Controller) sync(ctx context.Context) error {
	start := time.Now()

	// Fetch short-term predictions from algorithm service
	shortRecords, err := c.fetcher.FetchModel5ShortResults(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch model5 short results: %w", err)
	}

	// Apply short-term predictions (30min)
	shortCount, shortErrCount := c.applyShortTermPredictionsFromRecords(ctx, shortRecords)

	// Fetch long-term predictions from algorithm service
	longRecords, err := c.fetcher.FetchModel5LongResults(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch model5 long results: %w", err)
	}

	// Apply long-term predictions (24h)
	longCount, longErrCount := c.applyLongTermPredictionsFromRecords(ctx, longRecords)

	klog.InfoS("Replica prediction sync complete",
		"shortTotal", shortCount, "shortErrors", shortErrCount,
		"longTotal", longCount, "longErrors", longErrCount,
		"elapsed", time.Since(start))

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
