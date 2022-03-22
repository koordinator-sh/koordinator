package common

const (
	ConfigNameSpace     = "kube-system"
	SLOConfigMapName    = "slo-manager-config"
	ColocationConfigKey = "colocation-config"
)

const (
	DomainPrefix          = "koordinator.sh"
	LabelAnnotationPrefix = DomainPrefix + "/"

	LabelPodQoS      = LabelAnnotationPrefix + "qosClass"
	LabelPodPriority = LabelAnnotationPrefix + "priority"

	BatchCPU    = LabelAnnotationPrefix + "batch-cpu"
	BatchMemory = LabelAnnotationPrefix + "batch-memory"
)
