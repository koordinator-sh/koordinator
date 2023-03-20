package customevictor

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework/plugins/kubernetes/adaptor"
	"github.com/koordinator-sh/koordinator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	k8sdeschedulerframework "sigs.k8s.io/descheduler/pkg/framework"
	"sigs.k8s.io/descheduler/pkg/framework/plugins/defaultevictor"
)

type CustomEvictor struct {
	handle        framework.Handle
	evictorFilter k8sdeschedulerframework.EvictorPlugin
}

const (
	PluginName          = "CustomEvictor"
	EvictorLabelKey     = extension.DomainPrefix + "evicted"
	EvictorLabelTimeKey = extension.DomainPrefix + "evicted-time"
)

var _ framework.Evictor = &CustomEvictor{}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	defaultArgs := &defaultevictor.DefaultEvictorArgs{}
	if args == nil {
		args = defaultArgs
	}
	defaultEvictor, err := defaultevictor.New(args, adaptor.NewFrameworkHandleAdaptor(handle))
	if err != nil {
		return nil, err
	}
	return &CustomEvictor{
		handle:        handle,
		evictorFilter: defaultEvictor.(k8sdeschedulerframework.EvictorPlugin),
	}, nil
}

func (d *CustomEvictor) Name() string {
	return PluginName
}

func (d *CustomEvictor) Filter(pod *corev1.Pod) bool {
	return d.evictorFilter.Filter(pod)
}

func (d *CustomEvictor) PreEvictionFilter(pod *corev1.Pod) bool {
	return d.evictorFilter.PreEvictionFilter(pod)
}

func (d *CustomEvictor) Evict(ctx context.Context, pod *corev1.Pod, evictOptions framework.EvictOptions) bool {
	newPod := pod.DeepCopy()
	// write timestamp
	if newPod.Annotations == nil {
		newPod.Annotations = make(map[string]string)
	}
	newPod.Annotations[EvictorLabelKey] = "true"
	newPod.Annotations[EvictorLabelTimeKey] = strconv.FormatInt(time.Now().Unix(), 10)

	err := util.RetryOnConflictOrTooManyRequests(func() error {
		_, err1 := util.NewPatch().WithClientset(d.handle.ClientSet()).AddAnnotations(newPod.Annotations).PatchPod(pod)
		return err1
	})
	if err != nil {
		klog.V(4).InfoS(fmt.Sprintf("failed to patch pod for %s", evictOptions.PluginName),
			"pod", klog.KObj(pod), "err", err)
		return false
	}
	klog.InfoS(fmt.Sprintf("mark pod to be evicted success by %s", evictOptions.PluginName), "pod", klog.KObj(newPod))
	return true
}
