package core

import (
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
