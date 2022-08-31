package testutil

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

type FakeRecorder struct {
	eventReason string
}

func (f *FakeRecorder) Event(object runtime.Object, eventType, reason, message string) {
	f.eventReason = reason
	fmt.Printf("send event:eventType:%s,reason:%s,message:%s", eventType, reason, message)
}

func (f *FakeRecorder) Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...interface{}) {
	f.eventReason = reason
	fmt.Printf("send event:eventType:%s,reason:%s,message:%s", eventType, reason, messageFmt)
}

func (f *FakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventType, reason, messageFmt string, args ...interface{}) {
	f.Eventf(object, eventType, reason, messageFmt, args...)
}

func MockTestNode(cpu, memory string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-node",
			Namespace: "default",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
			},
		},
	}
}

func MockTestPod(qosClass apiext.QoSClass, name string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID(name),
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qosClass),
			},
		},
	}
}
