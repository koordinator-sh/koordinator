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

package evictor

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	sev1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/evictions"
	evictutils "github.com/koordinator-sh/koordinator/pkg/descheduler/evictions/utils"
)

func init() {
	RegisterEvictor(NativeEvictorName, NewNativeEvictor)
}

const (
	NativeEvictorName = "Eviction"
)

type NativeEvictor struct {
	client             kubernetes.Interface
	policyGroupVersion string
}

func NewNativeEvictor(client kubernetes.Interface) (Interface, error) {
	policyGroupVersion, err := evictutils.SupportEviction(client)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch eviction groupVersion: %v", err)
	}
	if len(policyGroupVersion) == 0 {
		return nil, fmt.Errorf("Server does not support eviction policy")
	}

	return &NativeEvictor{
		client:             client,
		policyGroupVersion: policyGroupVersion,
	}, nil
}

func (e *NativeEvictor) Evict(ctx context.Context, job *sev1alpha1.PodMigrationJob, pod *corev1.Pod) error {
	namespacedName := types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}
	klog.Infof("Try to evict Pod %q with MigrationJob %s", namespacedName, job.Name)
	err := evictions.EvictPod(ctx, e.client, pod, e.policyGroupVersion, job.Spec.DeleteOptions)
	if err != nil {
		klog.Errorf("Failed to evict Pod %q, err: %v", namespacedName, err)
	} else {
		klog.Infof("Evict Pod %q successfully", namespacedName)
	}
	return err
}
