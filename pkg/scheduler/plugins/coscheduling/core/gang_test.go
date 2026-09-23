package core

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func TestGang_resolveActivationRepresentative(t *testing.T) {
	tests := []struct {
		name       string
		pending    []string
		currentRep string
		wantPod    string // "" means nil
		wantRep    string
	}{
		{
			name:       "no pending children returns nil",
			pending:    nil,
			currentRep: "",
			wantPod:    "",
			wantRep:    "",
		},
		{
			name:       "empty representative picks a pending child",
			pending:    []string{"pod-1"},
			currentRep: "",
			wantPod:    "pod-1",
			wantRep:    "default/pod-1",
		},
		{
			name:       "valid representative is kept",
			pending:    []string{"pod-1", "pod-2"},
			currentRep: "default/pod-2",
			wantPod:    "pod-2",
			wantRep:    "default/pod-2",
		},
		{
			name:       "stale representative is reselected from pending children",
			pending:    []string{"pod-1"},
			currentRep: "default/gone",
			wantPod:    "pod-1",
			wantRep:    "default/pod-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gang := NewGang("default/ganga")
			for _, name := range tt.pending {
				gang.setChild(makePod("default", name))
			}
			gang.GangGroupInfo.RepresentativePodKey = tt.currentRep

			got := gang.resolveActivationRepresentative()

			assert.Equal(t, tt.wantRep, gang.GangGroupInfo.RepresentativePodKey)
			if tt.wantPod == "" {
				assert.Nil(t, got)
				return
			}
			assert.NotNil(t, got)
			assert.Equal(t, tt.wantPod, got.Name)
			// the fix guarantees the installed representative is a currently-pending child
			assert.NotNil(t, gang.PendingChildren[tt.wantRep])
			assert.Same(t, gang.PendingChildren[tt.wantRep], got)
		})
	}
}

// TestGang_resolveActivationRepresentative_Concurrent hammers resolveActivationRepresentative
// against the PendingChildren mutators that the scheduleOne loop / informer events drive, so the
// race detector can prove the representative maintenance stays synchronized (all accesses guard
// PendingChildren with gang.lock and the representative key with the gang group lock).
func TestGang_resolveActivationRepresentative_Concurrent(t *testing.T) {
	gang := NewGang("default/ganga")
	pods := make([]*corev1.Pod, 0, 16)
	for i := 0; i < 16; i++ {
		pod := makePod("default", fmt.Sprintf("pod-%d", i))
		pods = append(pods, pod)
		gang.setChild(pod)
	}

	const iterations = 2000
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				pod := pods[i%len(pods)]
				switch i % 3 {
				case 0:
					gang.addBoundPod(pod)
				case 1:
					gang.setChild(pod)
				case 2:
					gang.deletePod(pod)
				}
			}
		}()
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = gang.resolveActivationRepresentative()
			}
		}()
	}
	wg.Wait()
}
