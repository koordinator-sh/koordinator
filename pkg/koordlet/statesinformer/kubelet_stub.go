/*
Copyright 2022 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package statesinformer

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/transport"
)

type KubeletStub interface {
	GetAllPods() (corev1.PodList, error)
}

type kubeletStub struct {
	addr       string
	port       int
	httpClient *http.Client
}

func NewKubeletStub(addr string, port, timeoutSeconds int, token string) (KubeletStub, error) {
	preTlsConfig := makeTransportConfig(token, true)
	tlsConfig, err := transport.TLSConfigFor(preTlsConfig)
	if err != nil {
		return nil, err
	}
	rt := http.DefaultTransport
	if tlsConfig != nil {
		// If SSH Tunnel is turned on
		rt = utilnet.SetOldTransportDefaults(&http.Transport{
			TLSClientConfig: tlsConfig,
		})
	}
	roundTripper, err := transport.HTTPWrappersForConfig(makeTransportConfig(token, true), rt)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout:   time.Duration(timeoutSeconds) * time.Second,
		Transport: roundTripper,
	}
	return &kubeletStub{
		httpClient: client,
		addr:       addr,
		port:       port,
	}, nil
}

func makeTransportConfig(token string, insecure bool) *transport.Config {
	tlsConfig := &transport.Config{
		BearerToken: token,
		TLS: transport.TLSConfig{
			Insecure: true,
		},
	}
	return tlsConfig
}

func (k *kubeletStub) GetAllPods() (corev1.PodList, error) {
	podList := corev1.PodList{}
	url := fmt.Sprintf("https://%v:%d/pods/", k.addr, k.port)
	rsp, err := k.httpClient.Get(url)
	if err != nil {
		return podList, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return podList, fmt.Errorf("request %s failed, code %d", url, rsp.StatusCode)
	}

	body, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return podList, err
	}

	// parse json data
	err = json.Unmarshal(body, &podList)
	if err != nil {
		return podList, fmt.Errorf("parse kubelet pod list failed, err: %v", err)
	}
	return podList, nil
}
