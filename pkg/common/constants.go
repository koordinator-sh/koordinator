package common

const (
	ConfigNameSpace     = "kube-system"
	SLOConfigMapName    = "slo-manager-config"
	ColocationConfigKey = "colocation-config"
)

const (
	DomainPrefix          = "kccord.io"
	LabelAnnotationPrefix = DomainPrefix + "/"

	LabelPodQoS      = LabelAnnotationPrefix + "qosClass"
	LabelPodPriority = LabelAnnotationPrefix + "priority"

	BatchCPU    = LabelAnnotationPrefix + "batch-cpu"
	BatchMemory = LabelAnnotationPrefix + "batch-memory"
)
