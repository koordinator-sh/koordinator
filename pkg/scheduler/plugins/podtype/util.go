package podtype

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// IsValidPodType checks if a pod type is valid
func IsValidPodType(podType string) bool {
	switch strings.TrimSpace(strings.ToLower(podType)) {
	case PodTypeCPUIntensive, PodTypeMemoryIntensive, PodTypeIOIntensive, PodTypeNetworkIntensive:
		return true
	default:
		return false
	}
}

// getPodTypeFromPod gets the pod type from a pod
func getPodTypeFromPod(pod *corev1.Pod) string {
	if pod == nil || pod.Annotations == nil {
		return ""
	}
	if raw, ok := pod.Annotations[PodTypeAnnotationKey]; ok {
		raw = strings.TrimSpace(strings.ToLower(raw))
		if IsValidPodType(raw) {
			return raw
		}
	}
	return ""
}

// getPodTypeFromOwners resolves pod type by checking ownerReferences mapping in cache.
// It returns the first matched pod type or empty string if none found.
func getPodTypeFromOwners(pod *corev1.Pod, cache *PodTypeCache) string {
	if pod == nil || cache == nil {
		return ""
	}
	for _, owner := range pod.OwnerReferences {
		if owner.UID == "" {
			continue
		}
		if t := cache.GetOwnerPodTypeByUID(string(owner.UID)); t != "" {
			return strings.TrimSpace(strings.ToLower(t))
		}
	}
	return ""
}
