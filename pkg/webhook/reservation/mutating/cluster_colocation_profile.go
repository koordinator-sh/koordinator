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

package mutating

import (
	"context"
	"encoding/json"
	"math/rand"
	"sort"
	"strconv"

	admissionv1 "k8s.io/api/admission/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	configv1alpha1 "github.com/koordinator-sh/koordinator/apis/config/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

var (
	randIntnFn = rand.Intn
)

// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.koordinator.sh,resources=clustercolocationprofiles,verbs=get;list;watch

func (h *ReservationMutatingHandler) clusterColocationProfileMutatingReservation(ctx context.Context, req admission.Request, reservation *schedulingv1alpha1.Reservation) error {
	if req.Operation != admissionv1.Create {
		return nil
	}

	profileList := &configv1alpha1.ClusterColocationProfileList{}
	err := h.Client.List(ctx, profileList, utilclient.DisableDeepCopy)
	if err != nil {
		return err
	}

	if len(profileList.Items) == 0 {
		return nil
	}

	var matchedProfiles []*configv1alpha1.ClusterColocationProfile
	for i := range profileList.Items {
		profile := &profileList.Items[i]
		// Reservation does not support the namespaceSelector.
		if profile.Spec.Selector != nil {
			matched, err := h.matchObjectSelector(reservation, nil, profile.Spec.Selector)
			if !matched && err == nil {
				continue
			}
		}
		matchedProfiles = append(matchedProfiles, profile)
	}
	if len(matchedProfiles) == 0 {
		return nil
	}

	// sort the profile in lexicographic order
	sort.Slice(matchedProfiles, func(i, j int) bool {
		return matchedProfiles[i].Name < matchedProfiles[j].Name
	})
	for _, profile := range matchedProfiles {
		skip, err := shouldSkipProfile(profile)
		if err != nil {
			return err
		}
		if skip {
			klog.V(4).Infof("skip mutate Reservation %s/%s by clusterColocationProfile %s", reservation.Namespace, reservation.Name, profile.Name)
			continue
		}
		err = h.doMutateByColocationProfile(ctx, reservation, profile)
		if err != nil {
			return err
		}
		klog.V(4).Infof("mutate Reservation %s/%s by clusterColocationProfile %s", reservation.Namespace, reservation.Name, profile.Name)
	}
	// TODO: support mutate the resource spec for reservation
	return nil
}

func (h *ReservationMutatingHandler) matchObjectSelector(reservation, oldReservation *schedulingv1alpha1.Reservation, objectSelector *metav1.LabelSelector) (bool, error) {
	selector, err := util.GetFastLabelSelector(objectSelector)
	if err != nil {
		return false, err
	}
	if selector.Empty() {
		return true, nil
	}
	matched := selector.Matches(labels.Set(reservation.Labels))
	if !matched && oldReservation != nil {
		matched = selector.Matches(labels.Set(oldReservation.Labels))
	}
	return matched, nil
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

func (h *ReservationMutatingHandler) doMutateByColocationProfile(ctx context.Context, reservation *schedulingv1alpha1.Reservation, profile *configv1alpha1.ClusterColocationProfile) error {
	if len(profile.Spec.Labels) > 0 {
		if reservation.Labels == nil {
			reservation.Labels = make(map[string]string)
		}
		for k, v := range profile.Spec.Labels {
			reservation.Labels[k] = v
		}
	}

	if len(profile.Spec.Annotations) > 0 {
		if reservation.Annotations == nil {
			reservation.Annotations = make(map[string]string)
		}
		for k, v := range profile.Spec.Annotations {
			reservation.Annotations[k] = v
		}
	}

	if len(profile.Spec.LabelKeysMapping) > 0 {
		if reservation.Labels == nil {
			reservation.Labels = make(map[string]string)
		}
		for keyOld, keyNew := range profile.Spec.LabelKeysMapping {
			reservation.Labels[keyNew] = reservation.Labels[keyOld]
		}
	}

	if len(profile.Spec.AnnotationKeysMapping) > 0 {
		if reservation.Annotations == nil {
			reservation.Annotations = make(map[string]string)
		}
		for keyOld, keyNew := range profile.Spec.AnnotationKeysMapping {
			reservation.Annotations[keyNew] = reservation.Annotations[keyOld]
		}
	}

	if len(profile.Spec.LabelSuffixes) > 0 {
		if reservation.Labels == nil {
			reservation.Labels = make(map[string]string)
		}
		for key, suffix := range profile.Spec.LabelSuffixes {
			if _, ok := reservation.Labels[key]; ok {
				reservation.Labels[key] = reservation.Labels[key] + suffix
			}
		}
	}

	if profile.Spec.SchedulerName != "" && reservation.Spec.Template != nil {
		reservation.Spec.Template.Spec.SchedulerName = profile.Spec.SchedulerName
	}

	if profile.Spec.QoSClass != "" {
		if reservation.Labels == nil {
			reservation.Labels = make(map[string]string)
		}
		reservation.Labels[extension.LabelPodQoS] = profile.Spec.QoSClass
	}

	if profile.Spec.PriorityClassName != "" && reservation.Spec.Template != nil {
		priorityClass := &schedulingv1.PriorityClass{}
		err := h.Client.Get(ctx, types.NamespacedName{Name: profile.Spec.PriorityClassName}, priorityClass)
		if err != nil {
			return err
		}
		reservation.Spec.Template.Spec.PriorityClassName = profile.Spec.PriorityClassName
		reservation.Spec.Template.Spec.Priority = pointer.Int32(priorityClass.Value)
		reservation.Spec.Template.Spec.PreemptionPolicy = priorityClass.PreemptionPolicy
	}

	if profile.Spec.KoordinatorPriority != nil {
		if reservation.Labels == nil {
			reservation.Labels = make(map[string]string)
		}
		reservation.Labels[extension.LabelPodPriority] = strconv.FormatInt(int64(*profile.Spec.KoordinatorPriority), 10)
	}

	if profile.Spec.Patch.Raw != nil {
		cloneBytes, _ := json.Marshal(reservation)
		modified, err := strategicpatch.StrategicMergePatch(cloneBytes, profile.Spec.Patch.Raw, &schedulingv1alpha1.Reservation{})
		if err != nil {
			return err
		}
		newReservation := &schedulingv1alpha1.Reservation{}
		if err = json.Unmarshal(modified, newReservation); err != nil {
			return err
		}
		*reservation = *newReservation
	}

	return nil
}
