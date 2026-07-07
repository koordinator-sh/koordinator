package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
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

func TestGang_Helpers(t *testing.T) {
	gang := NewGang("default/gang-a")

	// 1. Test setGangFrom and isGangFromAnnotation
	gang.setGangFrom(GangFromPodAnnotation)
	assert.True(t, gang.isGangFromAnnotation())
	assert.Equal(t, GangFromPodAnnotation, gang.GangFrom)

	gang.setGangFrom(GangFromPodGroupCrd)
	assert.False(t, gang.isGangFromAnnotation())

	// 2. Test getGangWaitTime
	gang.WaitTime = 15 * time.Second
	assert.Equal(t, 15*time.Second, gang.getGangWaitTime())

	// 3. Test getPendingChildrenNum
	assert.Equal(t, 0, gang.getPendingChildrenNum())
	pod1 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "pod1"}}
	gang.PendingChildren["default/pod1"] = pod1
	assert.Equal(t, 1, gang.getPendingChildrenNum())

	// 4. Test getGangTotalNum
	gang.TotalChildrenNum = 4
	assert.Equal(t, 4, gang.getGangTotalNum())

	// 5. Test getGangAssumedPods and delAssumedPod
	assert.Equal(t, 0, gang.getGangAssumedPods())
	gang.addAssumedPod(pod1)
	assert.Equal(t, 1, gang.getGangAssumedPods())
	gang.delAssumedPod(pod1)
	assert.Equal(t, 0, gang.getGangAssumedPods())

	// 6. Test getCreateTime
	now := time.Now()
	gang.CreateTime = now
	assert.Equal(t, now, gang.getCreateTime())

	// 7. Test getChildrenFromGang
	assert.Equal(t, 0, len(gang.getChildrenFromGang()))
	gang.Children["default/pod1"] = pod1
	children := gang.getChildrenFromGang()
	assert.Equal(t, 1, len(children))
	assert.Equal(t, pod1, children[0])

	// 8. Test setBindingMembers / getBindingMembers
	assert.Nil(t, gang.getBindingMembers())
	setMembers := sets.New("default/pod1")
	gang.setBindingMembers(setMembers)
	assert.Equal(t, setMembers, gang.getBindingMembers())

	// 9. Test pickSomeChildren
	gang.PendingChildren["default/pod1"] = pod1
	picked := gang.pickSomeChildren()
	assert.Equal(t, pod1, picked)

	emptyGang := NewGang("empty")
	assert.Nil(t, emptyGang.pickSomeChildren())

	// 10. Test Representative helpers
	gang.GangGroupInfo = NewGangGroupInfo("default/gang-a", []string{"default/gang-a"})
	gang.GangGroupInfo.SetInitialized()
	err := gang.RecordIfNoRepresentatives(pod1)
	assert.NoError(t, err)
	assert.True(t, gang.IsPodRepresentative(pod1))

	// DeleteIfRepresentative
	gang.DeleteIfRepresentative(pod1, "test-delete")
	assert.False(t, gang.IsPodRepresentative(pod1))

	// ClearCurrentRepresentative
	err = gang.RecordIfNoRepresentatives(pod1)
	assert.NoError(t, err)
	assert.True(t, gang.IsPodRepresentative(pod1))
	gang.ClearCurrentRepresentative("test-clear")
	assert.False(t, gang.IsPodRepresentative(pod1))
}
