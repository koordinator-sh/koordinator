package nm

import (
	"fmt"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

const (
	ComponentLabelKey             = "app.kubernetes.io/component"
	NodeManagerComponentLabelName = "node-manager"
)

type NMPodWatcher struct {
	kubeletstub statesinformer.KubeletStub
}

func NewNMPodWater(kubeletstub statesinformer.KubeletStub) *NMPodWatcher {
	return &NMPodWatcher{kubeletstub: kubeletstub}
}

func (n *NMPodWatcher) GetNMPodEndpoint() (string, bool, error) {
	pods, err := n.kubeletstub.GetAllPods()
	if err != nil {
		return "", false, err
	}
	for _, pod := range pods.Items {
		if pod.Labels[ComponentLabelKey] != NodeManagerComponentLabelName {
			continue
		}
		if pod.Spec.HostNetwork == true {
			return "localhost:8042", true, nil
		}
		return fmt.Sprintf("%s:8042", pod.Status.PodIP), true, nil
	}
	return "", false, nil
}
