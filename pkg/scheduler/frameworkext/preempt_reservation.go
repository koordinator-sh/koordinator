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

package frameworkext

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework/preemption"

	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// WrapPreemptPodForReservation wraps the default PreemptPod function to support
// preempting reservation reserve pods by deleting the Reservation object
// instead of deleting the Pod (which may not exist for node-level reservations).
func WrapPreemptPodForReservation(
	defaultPreemptPod func(ctx context.Context, c preemption.Candidate, preemptor, victim *corev1.Pod, pluginName string) error,
	koordinatorClient koordinatorclientset.Interface,
) func(ctx context.Context, c preemption.Candidate, preemptor, victim *corev1.Pod, pluginName string) error {
	return func(ctx context.Context, c preemption.Candidate, preemptor, victim *corev1.Pod, pluginName string) error {
		if reservationutil.IsReservePod(victim) {
			rName := reservationutil.GetReservationNameFromReservePod(victim)
			if len(rName) == 0 {
				return fmt.Errorf("failed to get reservation name from reserve pod %s", klog.KObj(victim))
			}
			if err := koordinatorClient.SchedulingV1alpha1().
				Reservations().Delete(ctx, rName, metav1.DeleteOptions{}); err != nil {
				if !apierrors.IsNotFound(err) {
					return err
				}
				klog.V(4).InfoS("Reservation already deleted during preemption",
					"reservation", rName, "preemptor", klog.KObj(preemptor))
			}
			return nil
		}
		// Normal pod: use default PreemptPod
		return defaultPreemptPod(ctx, c, preemptor, victim, pluginName)
	}
}
