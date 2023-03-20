package customevictor

import (
	"context"
	"testing"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	frameworkruntime "github.com/koordinator-sh/koordinator/pkg/descheduler/framework/runtime"
	frameworktesting "github.com/koordinator-sh/koordinator/pkg/descheduler/framework/testing"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPodEvictor(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewSimpleClientset()
	fh, err := frameworktesting.NewFramework(
		[]frameworktesting.RegisterPluginFunc{
			func(reg *frameworkruntime.Registry, profile *deschedulerconfig.DeschedulerProfile) {
				reg.Register(PluginName, New)
				profile.Plugins.Evict.Enabled = append(profile.Plugins.Evict.Enabled, deschedulerconfig.Plugin{Name: PluginName})
				profile.Plugins.Filter.Enabled = append(profile.Plugins.Filter.Enabled, deschedulerconfig.Plugin{Name: PluginName})
			},
		},
		"test",
		frameworkruntime.WithClientSet(fakeClient),
	)
	assert.NoError(t, err)
	podEvictor := CustomEvictor{
		handle: fh,
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}
	pod, err = fakeClient.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	assert.NoError(t, err)
	assert.True(t, podEvictor.Evict(ctx, pod, framework.EvictOptions{}))
	newPo, err := fakeClient.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	v, ok := newPo.Annotations[EvictorLabelKey]
	assert.Equal(t, true, ok)
	assert.Equal(t, "true", v)
	_, ok = newPo.Annotations[EvictorLabelTimeKey]
	assert.Equal(t, true, ok)
}
