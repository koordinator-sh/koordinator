package podtype

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

var validPodTypes = map[string]struct{}{
	PodTypeCPUIntensive:     {},
	PodTypeMemoryIntensive:  {},
	PodTypeIOIntensive:      {},
	PodTypeNetworkIntensive: {},
}

// IsValidPodType checks if a pod type is valid
func IsValidPodType(podType string) bool {
	_, ok := validPodTypes[strings.TrimSpace(strings.ToLower(podType))]
	return ok
}

func parsePodTypes(raw string) []string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return nil
	}

	seen := map[string]struct{}{}
	var result []string
	appendType := func(t string) {
		if !IsValidPodType(t) {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		result = append(result, t)
	}

	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '+' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if IsValidPodType(part) {
			appendType(part)
			continue
		}
		if strings.HasSuffix(part, "-intensive") {
			base := strings.TrimSuffix(part, "-intensive")
			for _, token := range strings.Split(base, "-") {
				token = strings.TrimSpace(token)
				if token == "" {
					continue
				}
				appendType(token + "-intensive")
			}
		}
	}
	return result
}

// getPodTypesFromPod gets the pod types from a pod annotation.
func getPodTypesFromPod(pod *corev1.Pod) []string {
	if pod == nil || pod.Annotations == nil {
		return nil
	}
	raw, ok := pod.Annotations[PodTypeAnnotationKey]
	if !ok {
		return nil
	}
	return parsePodTypes(raw)
}

// getPodTypesFromOwners resolves pod types by checking ownerReferences mapping in cache.
func getPodTypesFromOwners(pod *corev1.Pod, cache *PodTypeCache) []string {
	if pod == nil || cache == nil {
		return nil
	}
	for _, owner := range pod.OwnerReferences {
		if owner.UID == "" {
			continue
		}
		if t := cache.GetOwnerPodTypesByUID(string(owner.UID)); len(t) > 0 {
			return t
		}
	}
	return nil
}

// compatibility helper
func getPodTypeFromPod(pod *corev1.Pod) string {
	types := getPodTypesFromPod(pod)
	if len(types) == 0 {
		return ""
	}
	return types[0]
}

func getPodTypeFromOwners(pod *corev1.Pod, cache *PodTypeCache) string {
	types := getPodTypesFromOwners(pod, cache)
	if len(types) == 0 {
		return ""
	}
	return types[0]
}
