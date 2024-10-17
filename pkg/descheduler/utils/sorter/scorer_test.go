package sorter

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestFunctions(t *testing.T) {
	resToWeightMap := map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    2,
		corev1.ResourceMemory: 3,
	}

	requested1 := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(1024, resource.BinarySI),
	}
	allocatable1 := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(2048, resource.BinarySI),
	}

	scorer1 := ResourceUsageScorer(resToWeightMap)
	score1 := scorer1(requested1, allocatable1)
	if score1 != 500 {
		t.Fatal("ResourceUsageScorer score1")
	}

	score2 := mostRequestedScore(800, 1000)
	if score2 != 800 {
		t.Fatal("ResourceUsageScorer score2")
	}
	score3 := mostRequestedScore(1200, 1000)
	if score3 != 1000 {
		t.Fatal("ResourceUsageScorer score3")
	}

	score4 := mostRequestedScore(1200, 0)
	if score4 != 0 {
		t.Fatal("ResourceUsageScorer score4")
	}

	resToWeightMap = map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    0,
		corev1.ResourceMemory: 0,
	}

	scorer1 = ResourceUsageScorer(resToWeightMap)
	score1 = scorer1(requested1, allocatable1)
	if score1 != 0 {
		t.Fatal("ResourceUsageScorer score5")
	}

	qCPU := resource.NewMilliQuantity(500, resource.DecimalSI)
	valueCPU := getResourceValue(corev1.ResourceCPU, *qCPU)
	if valueCPU != 500 {
		t.Fatal("ResourceUsageScorer score6")
	}

	qMem := resource.NewQuantity(1024, resource.BinarySI)
	valueMem := getResourceValue(corev1.ResourceMemory, *qMem)
	if valueMem != 1024 {
		t.Fatal("ResourceUsageScorer score7")
	}
}

func TestResourceUsageScorerPod(t *testing.T) {
	resToWeightMap := map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    0,
		corev1.ResourceMemory: 0,
	}

	scorer1 := ResourceUsageScorerPod(resToWeightMap)
	requested1 := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(1024, resource.BinarySI),
	}
	allocatable1 := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(2048, resource.BinarySI),
	}

	score1 := scorer1(requested1, allocatable1)
	if score1 != 0 {
		t.Fatal("TestResourceUsageScorerPod ResourceUsageScorerPod fail")
	}

	score := mostRequestedScorePod(100, 0)
	if score != 0 {
		t.Fatal("TestResourceUsageScorerPod mostRequestedScorePod fail")
	}
}
