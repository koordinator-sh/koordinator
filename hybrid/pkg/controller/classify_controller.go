/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-03 @author yangwanjin
 */

package controller

import (
	"context"
	"fmt"
	"path"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"hybrid/pkg/constants"
	"hybrid/pkg/predictor"
	"hybrid/pkg/utils"
)

type ClassifyController struct {
	clientset      *kubernetes.Clientset
	predictionFile string
	syncInterval   time.Duration
	stopCh         chan struct{}
}

func NewClassifyController(clientset *kubernetes.Clientset, predictionFile string, syncInterval time.Duration) *ClassifyController {
	if predictionFile == "" {
		predictionFile = path.Join(constants.DefaultOutputDir, constants.DefaultPredictionFile)
	} else {
		predictionFile = path.Join(constants.DefaultOutputDir, predictionFile)
	}
	if syncInterval <= 0 {
		syncInterval = constants.DefaultSyncInterval
	}

	return &ClassifyController{
		clientset:      clientset,
		predictionFile: predictionFile,
		syncInterval:   syncInterval,
		stopCh:         make(chan struct{}),
	}
}

func (c *ClassifyController) Start(ctx context.Context) error {
	klog.Infof("Starting ClassifyController with prediction file: %s, sync interval: %v",
		c.predictionFile, c.syncInterval)

	if err := c.syncClassifications(ctx); err != nil {
		klog.Errorf("Initial sync failed: %v", err)
	}

	ticker := time.NewTicker(c.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			klog.Info("Context cancelled, stopping ClassifyController")
			return nil
		case <-c.stopCh:
			klog.Info("Stop signal received, stopping ClassifyController")
			return nil
		case <-ticker.C:
			klog.V(4).Info("Ticker triggered, starting sync cycle")
			if err := c.syncClassifications(ctx); err != nil {
				klog.Errorf("Sync failed: %v", err)
			}
		}
	}
}

func (c *ClassifyController) Stop() {
	close(c.stopCh)
}

// syncPredictions read predictions from loader and sync to workloads
func (c *ClassifyController) syncClassifications(ctx context.Context) error {
	klog.V(4).Infof("Starting prediction sync at %v", time.Now())

	predictions, err := predictor.ParsePredictorFile(c.predictionFile)
	if err != nil {
		return fmt.Errorf("failed to parse prediction file: %w", err)
	}

	for _, record := range predictions {
		if err := c.syncWorkloadAnnotation(ctx, record); err != nil {
			klog.Errorf("Failed to sync pod %s/%s: %v", record.Namespace, record.Name, err)
		} else {
			klog.V(4).Infof("Successfully synced workload with type: %s", record.PredictedType)
		}
	}

	return nil
}

func (c *ClassifyController) syncWorkloadAnnotation(ctx context.Context, record predictor.PodRecord) error {
	timestamp := time.Now().Format(time.RFC3339)
	annotations := map[string]string{
		predictor.AnnotationPredictedType: record.PredictedType,
		predictor.AnnotationTimestamp:     timestamp,
	}

	controllerInfo, err := utils.GetControllerInfoForPod(ctx, c.clientset, record.Namespace, record.Name)
	if err != nil {
		return err
	}

	switch controllerInfo.Kind {
	case "Deployment":
		if err := c.updateDeploymentAnnotations(ctx, record.Namespace, controllerInfo.Name, annotations); err == nil {
			return nil
		} else if !errors.IsNotFound(err) {
			klog.V(4).Infof("Error updating Deployment %s/%s: %v", record.Namespace, controllerInfo.Name, err)
		}
	case "StatefulSet":
		if err := c.updateStatefulSetAnnotations(ctx, record.Namespace, controllerInfo.Name, annotations); err == nil {
			return nil
		} else if !errors.IsNotFound(err) {
			klog.V(4).Infof("Error updating StatefulSet %s/%s: %v", record.Namespace, controllerInfo.Name, err)
		}
	case "DaemonSet":
		if err := c.updateDaemonSetAnnotations(ctx, record.Namespace, controllerInfo.Name, annotations); err == nil {
			return nil
		} else if !errors.IsNotFound(err) {
			klog.V(4).Infof("Error updating DaemonSet %s/%s: %v", record.Namespace, controllerInfo.Name, err)
		}
	case "CronJob":
		if err := c.updateCronJobAnnotations(ctx, record.Namespace, controllerInfo.Name, annotations); err == nil {
			return nil
		} else if !errors.IsNotFound(err) {
			klog.V(4).Infof("Error updating CronJob %s/%s: %v", record.Namespace, controllerInfo.Name, err)
		}
	case "Job":
		if err := c.updateJobAnnotations(ctx, record.Namespace, controllerInfo.Name, annotations); err == nil {
			return nil
		} else if !errors.IsNotFound(err) {
			klog.V(4).Infof("Error updating Job %s/%s: %v", record.Namespace, controllerInfo.Name, err)
		}
	case "ReplicaSet":
		if err := c.updateReplicaSetAnnotations(ctx, record.Namespace, controllerInfo.Name, annotations); err == nil {
			return nil
		} else if !errors.IsNotFound(err) {
			klog.V(4).Infof("Error updating ReplicaSet %s/%s: %v", record.Namespace, controllerInfo.Name, err)
		}
	default:
		return fmt.Errorf("workload not found: %s/%s ", record.Namespace, record.Name)
	}
	return nil
}

func (c *ClassifyController) updateDeploymentAnnotations(ctx context.Context, namespace, name string, annotations map[string]string) error {
	deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if !c.needsUpdate(deployment.Annotations, annotations) {
		return nil
	}

	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}

	for key, value := range annotations {
		deployment.Annotations[key] = value
	}

	_, err = c.clientset.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Deployment: %w", err)
	}

	return nil
}

func (c *ClassifyController) updateStatefulSetAnnotations(ctx context.Context, namespace, name string, annotations map[string]string) error {
	statefulSet, err := c.clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if !c.needsUpdate(statefulSet.Annotations, annotations) {
		return nil
	}

	if statefulSet.Annotations == nil {
		statefulSet.Annotations = make(map[string]string)
	}

	for key, value := range annotations {
		statefulSet.Annotations[key] = value
	}

	_, err = c.clientset.AppsV1().StatefulSets(namespace).Update(ctx, statefulSet, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update StatefulSet: %w", err)
	}

	return nil
}

func (c *ClassifyController) updateDaemonSetAnnotations(ctx context.Context, namespace, name string, annotations map[string]string) error {
	daemonSet, err := c.clientset.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !c.needsUpdate(daemonSet.Annotations, annotations) {
		return nil
	}

	if daemonSet.Annotations == nil {
		daemonSet.Annotations = make(map[string]string)
	}

	for key, value := range annotations {
		daemonSet.Annotations[key] = value
	}

	_, err = c.clientset.AppsV1().DaemonSets(namespace).Update(ctx, daemonSet, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update DaemonSet: %w", err)
	}

	return nil
}

func (c *ClassifyController) updateCronJobAnnotations(ctx context.Context, namespace, name string, annotations map[string]string) error {
	cronJob, err := c.clientset.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !c.needsUpdate(cronJob.Annotations, annotations) {
		return nil
	}

	if cronJob.Annotations == nil {
		cronJob.Annotations = make(map[string]string)
	}

	for key, value := range annotations {
		cronJob.Annotations[key] = value
	}

	_, err = c.clientset.BatchV1().CronJobs(namespace).Update(ctx, cronJob, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update CronJob: %w", err)
	}

	return nil
}

func (c *ClassifyController) updateJobAnnotations(ctx context.Context, namespace, name string, annotations map[string]string) error {
	job, err := c.clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !c.needsUpdate(job.Annotations, annotations) {
		return nil
	}

	if job.Annotations == nil {
		job.Annotations = make(map[string]string)
	}

	for key, value := range annotations {
		job.Annotations[key] = value
	}

	_, err = c.clientset.BatchV1().Jobs(namespace).Update(ctx, job, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Job: %w", err)
	}

	return nil
}

func (c *ClassifyController) updateReplicaSetAnnotations(ctx context.Context, namespace, name string, annotations map[string]string) error {
	replicaSet, err := c.clientset.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if !c.needsUpdate(replicaSet.Annotations, annotations) {
		return nil
	}

	if replicaSet.Annotations == nil {
		replicaSet.Annotations = make(map[string]string)
	}

	for key, value := range annotations {
		replicaSet.Annotations[key] = value
	}

	_, err = c.clientset.AppsV1().ReplicaSets(namespace).Update(ctx, replicaSet, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ReplicaSet: %w", err)
	}

	return nil
}

func (c *ClassifyController) needsUpdate(existing, new map[string]string) bool {
	if existing == nil {
		return true
	}
	for key, newValue := range new {
		if key == predictor.AnnotationTimestamp {
			continue
		}
		if existingValue, ok := existing[key]; !ok || existingValue != newValue {
			return true
		}
	}
	return false
}
