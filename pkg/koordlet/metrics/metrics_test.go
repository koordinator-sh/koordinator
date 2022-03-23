package metrics

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenNodeLabels(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test",
			Labels: map[string]string{},
		},
	}
	Register(node)
	defer Register(nil)
	labels := genNodeLabels()
	assert.Equal(t, 1, len(labels))
	assert.Equal(t, "test", labels[NodeKey])
	RecordCollectNodeCPUInfoStatus(nil)
}

func TestCommonCollectors(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("200"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("200"),
			},
		},
	}
	testingErr := fmt.Errorf("test error")
	testingNow := time.Now()

	t.Run("test not panic", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)

		RecordKoordletStartTime(testingNode.Name, float64(testingNow.Unix()))
		RecordCollectNodeCPUInfoStatus(testingErr)
		RecordCollectNodeCPUInfoStatus(nil)
		RecordBESuppressCores("cfsQuota", float64(1000))
		RecordPodEviction("evictByCPU")
	})
}
