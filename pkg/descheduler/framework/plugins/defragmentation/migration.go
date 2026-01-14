/*
Copyright 2022 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package defragmentation

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
)

// MigrationStatus represents the status of a migration
type MigrationStatus struct {
	Task      *MigrationTask
	Status    string
	StartTime time.Time
	EndTime   time.Time
	Error     error
	Retries   int
}

// MigrationManager manages pod migrations
type MigrationManager struct {
	client            kubernetes.Interface
	config            *config.DefragmentationSafetyConfig
	ongoingMigrations sync.Map // map[string]*MigrationStatus
}

// NewMigrationManager creates a new MigrationManager
func NewMigrationManager(
	client kubernetes.Interface,
	config *config.DefragmentationSafetyConfig,
) *MigrationManager {
	return &MigrationManager{
		client: client,
		config: config,
	}
}

// ExecuteMigration executes a migration task
func (m *MigrationManager) ExecuteMigration(
	ctx context.Context,
	task *MigrationTask,
) error {

	key := fmt.Sprintf("%s/%s", task.Pod.Namespace, task.Pod.Name)

	// Record migration start
	status := &MigrationStatus{
		Task:      task,
		Status:    "InProgress",
		StartTime: time.Now(),
	}
	m.ongoingMigrations.Store(key, status)

	defer func() {
		status.EndTime = time.Now()
		m.ongoingMigrations.Delete(key)
	}()

	klog.V(3).InfoS("Starting pod migration",
		"pod", klog.KObj(task.Pod),
		"sourceNode", task.SourceNode,
		"targetNode", task.TargetNode,
		"reason", task.Reason,
	)

	// Use Koordinator's PodMigrationJob for migration
	migrationJob := &schedulingv1alpha1.PodMigrationJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("defrag-%s-%d", task.Pod.Name, time.Now().Unix()),
			Namespace: task.Pod.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":   "koordinator-descheduler",
				"koordinator.sh/defragmentation": "true",
			},
		},
		Spec: schedulingv1alpha1.PodMigrationJobSpec{
			PodRef: &corev1.ObjectReference{
				Namespace: task.Pod.Namespace,
				Name:      task.Pod.Name,
				UID:       task.Pod.UID,
			},
			ReservationOptions: &schedulingv1alpha1.PodMigrateReservationOptions{
				ReservationRef: &corev1.ObjectReference{
					Name: fmt.Sprintf("defrag-reservation-%s", task.Pod.Name),
				},
				Template: &schedulingv1alpha1.ReservationTemplateSpec{
					Spec: schedulingv1alpha1.ReservationSpec{
						Template: &corev1.PodTemplateSpec{
							Spec: task.Pod.Spec,
						},
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Namespace: task.Pod.Namespace,
									Name:      task.Pod.Name,
								},
							},
						},
						TTL: &metav1.Duration{Duration: 10 * time.Minute},
					},
				},
			},
			Mode: schedulingv1alpha1.PodMigrationJobModeReservationFirst,
			TTL:  &metav1.Duration{Duration: m.config.MigrationTimeout.Duration},
		},
	}

	// If target node is specified, add node affinity
	if task.TargetNode != "" {
		if migrationJob.Spec.ReservationOptions.Template.Spec.Template == nil {
			migrationJob.Spec.ReservationOptions.Template.Spec.Template = &corev1.PodTemplateSpec{}
		}
		if migrationJob.Spec.ReservationOptions.Template.Spec.Template.Spec.Affinity == nil {
			migrationJob.Spec.ReservationOptions.Template.Spec.Template.Spec.Affinity = &corev1.Affinity{}
		}
		if migrationJob.Spec.ReservationOptions.Template.Spec.Template.Spec.Affinity.NodeAffinity == nil {
			migrationJob.Spec.ReservationOptions.Template.Spec.Template.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
		}

		migrationJob.Spec.ReservationOptions.Template.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchFields: []corev1.NodeSelectorRequirement{
						{
							Key:      "metadata.name",
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{task.TargetNode},
						},
					},
				},
			},
		}
	}

	// Create migration job
	// Note: This requires the Koordinator CRD client, which should be injected
	// For now, this is a placeholder showing the structure
	klog.V(4).InfoS("Would create migration job", "job", migrationJob)

	// Wait for migration completion
	if err := m.waitForMigrationCompletion(ctx, migrationJob); err != nil {
		status.Status = "Failed"
		status.Error = err

		// Check if retry is needed
		if status.Retries < m.config.MaxRetries {
			status.Retries++
			klog.V(4).InfoS("Retrying migration",
				"pod", klog.KObj(task.Pod),
				"retry", status.Retries,
				"maxRetries", m.config.MaxRetries,
			)

			time.Sleep(m.config.RetryInterval.Duration)
			return m.ExecuteMigration(ctx, task)
		}

		return err
	}

	status.Status = "Succeeded"
	klog.V(3).InfoS("Pod migration completed successfully",
		"pod", klog.KObj(task.Pod),
		"duration", time.Since(status.StartTime),
	)

	return nil
}

// waitForMigrationCompletion waits for migration to complete
func (m *MigrationManager) waitForMigrationCompletion(
	ctx context.Context,
	migrationJob *schedulingv1alpha1.PodMigrationJob,
) error {

	timeout := m.config.MigrationTimeout.Duration
	if timeout == 0 {
		timeout = 10 * time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("migration timeout after %v", timeout)

		case <-ticker.C:
			// TODO: Get latest status from PodMigrationJob CRD
			// This is a placeholder implementation
			klog.V(6).InfoS("Waiting for migration to complete",
				"migrationJob", migrationJob.Name,
			)

			// For now, simulate completion after a short delay
			// In real implementation, this should query the actual CRD status
			return nil
		}
	}
}

// CountOngoingMigrations counts ongoing migrations
func (m *MigrationManager) CountOngoingMigrations() int {
	count := 0
	m.ongoingMigrations.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// GetMigrationStatus gets the status of a migration
func (m *MigrationManager) GetMigrationStatus(namespace, name string) *MigrationStatus {
	key := fmt.Sprintf("%s/%s", namespace, name)
	if value, ok := m.ongoingMigrations.Load(key); ok {
		return value.(*MigrationStatus)
	}
	return nil
}

// GetAllMigrationStatuses gets all migration statuses
func (m *MigrationManager) GetAllMigrationStatuses() []*MigrationStatus {
	statuses := make([]*MigrationStatus, 0)
	m.ongoingMigrations.Range(func(key, value interface{}) bool {
		statuses = append(statuses, value.(*MigrationStatus))
		return true
	})
	return statuses
}
