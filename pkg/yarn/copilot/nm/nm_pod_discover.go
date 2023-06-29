package nm

import (
	"fmt"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

const (
	COMPONENT_LABEL_KEY              = "app.kubernetes.io/component"
	NODEMANAGER_COMPONENT_LABEL_NAME = "node-manager"
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
		if pod.Labels[COMPONENT_LABEL_KEY] != NODEMANAGER_COMPONENT_LABEL_NAME {
			continue
		}
		if pod.Spec.HostNetwork == true {
			return "localhost:8042", true, nil
		}
		return fmt.Sprintf(pod.Status.PodIP), true, nil
	}
	return "", false, nil
}
