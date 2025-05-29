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

package colocationprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

var (
	randIntnFn = rand.Intn
)

func (r *Reconciler) listPodsForProfile(profile *configv1alpha1.ClusterColocationProfile) (*corev1.PodList, error) {
	// TODO: support namespaceSelector and merge the matched pods
	if profile.Spec.Selector == nil { // match nothing
		return nil, nil
	}

	// list pods with label selectors
	ps := profile.Spec.Selector
	labelSelector, err := metav1.LabelSelectorAsSelector(ps)
	if err != nil {
		return nil, fmt.Errorf("failed to generate selector %+v, err: %w", ps, err)
	}

	podList := &corev1.PodList{}
	// NOTE: Only handle pending pods.
	if err = r.Client.List(context.TODO(), podList, &client.ListOptions{
		LabelSelector: labelSelector,
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", ""),
	}, utilclient.DisableDeepCopy); err != nil {
		return nil, fmt.Errorf("list pods failed for selector %+v, err: %w", ps, err)
	}

	return podList, nil
}

func (r *Reconciler) updatePodByClusterColocationProfile(ctx context.Context, profile *configv1alpha1.ClusterColocationProfile, pod *corev1.Pod) (bool, error) {
	modifiedPod := pod.DeepCopy()
	err := r.doMutateByColocationProfile(modifiedPod, profile)
	if err != nil {
		return false, fmt.Errorf("failed to mutate pod, err: %w", err)
	}
	if reflect.DeepEqual(pod, modifiedPod) {
		return false, nil
	}

	err = util.RetryOnConflictOrTooManyRequests(func() error {
		patchErr := r.Client.Patch(ctx, modifiedPod, client.MergeFrom(pod))
		if patchErr != nil {
			klog.V(5).InfoS("failed to patch pod", "pod", klog.KObj(pod), "err", patchErr)
			return patchErr
		}
		klog.V(6).InfoS("successfully patch pod", "pod", klog.KObj(pod), "modifiedPod", util.DumpJSON(modifiedPod))
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("failed tp patch pod, err: %w", err)
	}

	klog.V(4).InfoS("successfully patch pod for clusterColocationProfile", "profile", profile.Name, "pod", klog.KObj(pod))
	return true, nil
}

func (r *Reconciler) doMutateByColocationProfile(pod *corev1.Pod, profile *configv1alpha1.ClusterColocationProfile) error {
	if len(profile.Spec.Labels) > 0 {
		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}
		for k, v := range profile.Spec.Labels {
			pod.Labels[k] = v
		}
	}

	if len(profile.Spec.Annotations) > 0 {
		if pod.Annotations == nil {
			pod.Annotations = make(map[string]string)
		}
		for k, v := range profile.Spec.Annotations {
			pod.Annotations[k] = v
		}
	}

	if len(profile.Spec.LabelKeysMapping) > 0 {
		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}
		for keyOld, keyNew := range profile.Spec.LabelKeysMapping {
			pod.Labels[keyNew] = pod.Labels[keyOld]
		}
	}

	if len(profile.Spec.AnnotationKeysMapping) > 0 {
		if pod.Annotations == nil {
			pod.Annotations = make(map[string]string)
		}
		for keyOld, keyNew := range profile.Spec.AnnotationKeysMapping {
			pod.Annotations[keyNew] = pod.Annotations[keyOld]
		}
	}

	if profile.Spec.QoSClass != "" {
		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}
		pod.Labels[extension.LabelPodQoS] = profile.Spec.QoSClass
	}

	if profile.Spec.KoordinatorPriority != nil {
		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}
		pod.Labels[extension.LabelPodPriority] = strconv.FormatInt(int64(*profile.Spec.KoordinatorPriority), 10)
	}

	if profile.Spec.Patch.Raw != nil {
		cloneBytes, _ := json.Marshal(pod)
		modified, err := strategicpatch.StrategicMergePatch(cloneBytes, profile.Spec.Patch.Raw, &corev1.Pod{})
		if err != nil {
			return err
		}
		newPod := &corev1.Pod{}
		if err = json.Unmarshal(modified, newPod); err != nil {
			return err
		}
		*pod = *newPod
	}

	// NOTE: Below fields are not supported by colocation-profile controller:
	// - PriorityClassName
	// - SchedulerName
	return nil
}

type ReconcileSummary struct {
	Time        string
	Profile     string
	Desired     int
	Succeeded   int
	Changed     int
	RateLimited int
	Skipped     int
	Cached      int
}

func newSummary(profileName string) *ReconcileSummary {
	return &ReconcileSummary{
		Time:    time.Now().String(),
		Profile: profileName,
	}
}

func (s *ReconcileSummary) IsAllSucceeded() bool {
	return s.Succeeded >= s.Desired
}

func shouldSkipProfile(profile *configv1alpha1.ClusterColocationProfile) (bool, error) {
	percent := 100
	if profile.Spec.Probability != nil {
		var err error
		percent, err = intstr.GetScaledValueFromIntOrPercent(profile.Spec.Probability, 100, false)
		if err != nil {
			return false, err
		}
	}
	return percent == 0 || (percent != 100 && randIntnFn(100) > percent), nil
}

func getPodUpdateKey(profile *configv1alpha1.ClusterColocationProfile, pod *corev1.Pod) string {
	return profile.ResourceVersion + "/" + string(pod.UID)
}
