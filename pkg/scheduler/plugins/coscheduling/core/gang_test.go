package core

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGang_hasGangInit(t *testing.T) {
	gang := NewGang("test-gang")

	assert.False(t, gang.hasGangInit(), "new gang should not be initialized")

	gang.lock.Lock()
	gang.HasGangInit = true
	gang.lock.Unlock()

	assert.True(t, gang.hasGangInit(), "gang should be initialized after HasGangInit set")
}

// TestGang_hasGangInitConcurrent verifies there is no data race between concurrent
// writers (simulating tryInitByPodConfig/tryInitByPodGroup holding lock.Lock) and
// concurrent readers (simulating PreEnqueue/BeforePreFilter using hasGangInit).
// Run with -race to exercise the detector.
func TestGang_hasGangInitConcurrent(t *testing.T) {
	gang := NewGang("test-gang-concurrent")

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n * 2)

	for i := 0; i < n; i++ {
		go func(v bool) {
			defer wg.Done()
			gang.lock.Lock()
			gang.HasGangInit = v
			gang.lock.Unlock()
		}(i%2 == 0)
	}

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = gang.hasGangInit()
		}()
	}

	wg.Wait()
}

func TestGang_getWaitingChildrenFromGang(t *testing.T) {
	tests := []struct {
		name         string
		wantChildren []*corev1.Pod
	}{
		{
			name: "normal flow",
			wantChildren: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pod1",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gang := NewGang("gang")
			for i := range tt.wantChildren {
				gang.addAssumedPod(tt.wantChildren[i])
			}
			assert.Equalf(t, tt.wantChildren, gang.getWaitingChildrenFromGang(), "getWaitingChildrenFromGang()")
		})
	}
}
