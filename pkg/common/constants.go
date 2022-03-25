package common

const (
	ConfigNameSpace     = "koord-system"
	SLOCtrlConfigMap    = "slo-controller-config"
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
