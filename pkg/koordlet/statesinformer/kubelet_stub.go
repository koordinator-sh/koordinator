package statesinformer

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

type KubeletStub interface {
	GetAllPods() (corev1.PodList, error)
}

type kubeletStub struct {
	ipAddr         string
	httpPort       int
	timeoutSeconds int
}

func NewKubeletStub(ip string, port, timeoutSeconds int) KubeletStub {
	return &kubeletStub{
		ipAddr:         ip,
		httpPort:       port,
		timeoutSeconds: timeoutSeconds,
	}
}

func (k *kubeletStub) GetAllPods() (corev1.PodList, error) {
	podList := corev1.PodList{}
	result, err := util.DoHTTPGet("pods", k.ipAddr, k.httpPort, k.timeoutSeconds)
	if err != nil {
		return podList, err
	}
	// parse json data
	err = json.Unmarshal(result, &podList)
	if err != nil {
		return podList, fmt.Errorf("parse kubelet pod list failed, err: %v", err)
	}
	return podList, nil
}
