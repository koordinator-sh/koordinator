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

package kubelet

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

type KubeletStub interface {
	GetAllPods() (corev1.PodList, error)
	GetKubeletConfiguration() (*kubeletconfiginternal.KubeletConfiguration, error)
}

type kubeletStub struct {
	addr       string
	port       int
	scheme     string
	httpClient *http.Client
}

type KubeletStubConfig struct {
	KubeletPreferredAddressType string
	KubeletSyncTimeout          time.Duration
	InsecureKubeletTLS          bool
	KubeletReadOnlyPort         uint
}

func NewKubeletStubDefaultConfig() *KubeletStubConfig {
	return &KubeletStubConfig{
		KubeletPreferredAddressType: string(corev1.NodeInternalIP),
		KubeletSyncTimeout:          3 * time.Second,
		InsecureKubeletTLS:          false,
		KubeletReadOnlyPort:         10255,
	}
}

func (c *KubeletStubConfig) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.KubeletPreferredAddressType, "kubelet-preferred-address-type", c.KubeletPreferredAddressType, "The node address types to use when determining which address to use to connect to a particular node.")
	fs.DurationVar(&c.KubeletSyncTimeout, "kubelet-sync-timeout", c.KubeletSyncTimeout, "The length of time to wait before giving up on a single request to Kubelet. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
	fs.BoolVar(&c.InsecureKubeletTLS, "kubelet-insecure-tls", c.InsecureKubeletTLS, "Using read-only port to communicate with Kubelet. For testing purposes only, not recommended for production use.")
	fs.UintVar(&c.KubeletReadOnlyPort, "kubelet-read-only-port", c.KubeletReadOnlyPort, "The read-only port for the kubelet to serve on with no authentication/authorization. Default: 10255.")
}

func NewKubeletStubFromConfig(node *corev1.Node, cfg *KubeletStubConfig) (KubeletStub, error) {
	var address string
	var err error
	var port int
	var scheme string
	var restConfig *rest.Config

	addressPreferredType := corev1.NodeAddressType(cfg.KubeletPreferredAddressType)
	// if the address of the specified type has not been set or error type, InternalIP will be used.
	if !util.IsNodeAddressTypeSupported(addressPreferredType) {
		klog.Warningf("Wrong address type or empty type, InternalIP will be used, error: (%+v).", addressPreferredType)
		addressPreferredType = corev1.NodeInternalIP
	}
	address, err = util.GetNodeAddress(node, addressPreferredType)
	if err != nil {
		klog.Fatalf("Get node address error: %v type(%s) ", err, cfg.KubeletPreferredAddressType)
		return nil, err
	}

	if cfg.InsecureKubeletTLS {
		port = int(cfg.KubeletReadOnlyPort)
		scheme = "https"
	} else {
		restConfig, err = config.GetConfig()
		if err != nil {
			return nil, err
		}
		restConfig.TLSClientConfig.Insecure = true
		restConfig.TLSClientConfig.CAData = nil
		restConfig.TLSClientConfig.CAFile = ""
		port = int(node.Status.DaemonEndpoints.KubeletEndpoint.Port)
		scheme = "http"
	}

	return NewKubeletStub(address, port, scheme, cfg.KubeletSyncTimeout, restConfig)
}

func NewKubeletStub(addr string, port int, scheme string, timeout time.Duration, cfg *rest.Config) (KubeletStub, error) {
	client := &http.Client{
		Timeout: timeout,
	}
	if cfg != nil && rest.IsConfigTransportTLS(*cfg) {
		transport, err := rest.TransportFor(cfg)
		if err != nil {
			return nil, err
		}
		client.Transport = transport
	}

	return &kubeletStub{
		httpClient: client,
		addr:       addr,
		port:       port,
		scheme:     scheme,
	}, nil
}

func (k *kubeletStub) GetAllPods() (corev1.PodList, error) {
	url := url.URL{
		Scheme: k.scheme,
		Host:   net.JoinHostPort(k.addr, strconv.Itoa(k.port)),
		Path:   "/pods/",
	}
	podList := corev1.PodList{}
	rsp, err := k.httpClient.Get(url.String())
	if err != nil {
		return podList, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return podList, fmt.Errorf("request %s failed, code %d", url.String(), rsp.StatusCode)
	}

	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		return podList, err
	}

	// parse json data
	err = json.Unmarshal(body, &podList)
	if err != nil {
		return podList, fmt.Errorf("failed to parse kubelet pod list, err: %v", err)
	}
	return podList, nil
}

func (k *kubeletStub) GetKubeletConfiguration() (*kubeletconfiginternal.KubeletConfiguration, error) {
	url := url.URL{
		Scheme: k.scheme,
		Host:   net.JoinHostPort(k.addr, strconv.Itoa(k.port)),
		Path:   "/configz",
	}
	var kubeletConfiguration kubeletconfiginternal.KubeletConfiguration
	rsp, err := k.httpClient.Get(url.String())
	if err != nil {
		return nil, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request %s failed, code %d", url.String(), rsp.StatusCode)
	}

	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		return nil, err
	}

	// parse json data
	err = json.Unmarshal(body, &kubeletConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to parse KubeletConfiguration, err: %v", err)
	}
	return &kubeletConfiguration, nil
}
