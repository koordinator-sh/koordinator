package server

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/koordinator-sh/koordinator/pkg/yarn/copilot/nm"
)

func ParseContainerInfo(yarnContainer *nm.YarnContainer, op *nm.NodeMangerOperator) *ContainerInfo {
	return &ContainerInfo{
		Name:        yarnContainer.Id,
		Namespace:   "yarn",
		UID:         yarnContainer.Id,
		CgroupDir:   op.GenerateCgroupPath(yarnContainer.Id),
		HostNetwork: true,
		Resources: corev1.ResourceRequirements{
			Limits: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(yarnContainer.TotalVCoresNeeded*1000), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewMilliQuantity(int64(yarnContainer.TotalMemoryNeededMB*1024*1024*1000), resource.DecimalSI),
			},
			Requests: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(yarnContainer.TotalVCoresNeeded*1000), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewMilliQuantity(int64(yarnContainer.TotalMemoryNeededMB*1024*1024*1000), resource.DecimalSI),
			},
		},
	}
}
