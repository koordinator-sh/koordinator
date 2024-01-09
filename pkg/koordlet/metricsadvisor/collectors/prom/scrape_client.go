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

package prom

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
)

type ScrapeClient interface {
	URL() string
	GetMetrics() ([]*model.Sample, error)
}

type ClientConfig struct {
	Timeout     time.Duration
	InsecureTLS bool
	Address     string
	Port        int
	Path        string
}

type ScrapeClientImpl struct {
	httpClient *http.Client
	url        *url.URL
	buffers    *sync.Pool
}

func NewScrapeClientFromConfig(cfg *ClientConfig) (ScrapeClient, error) {
	var (
		scheme     string
		restConfig *rest.Config
		err        error
	)

	if cfg.InsecureTLS {
		scheme = "http"
	} else {
		restConfig, err = config.GetConfig()
		if err != nil {
			return nil, err
		}
		restConfig.TLSClientConfig.Insecure = true
		restConfig.TLSClientConfig.CAData = nil
		restConfig.TLSClientConfig.CAFile = ""
		scheme = "https"
	}

	return NewScrapeClient(cfg.Address, cfg.Port, cfg.Path, scheme, cfg.Timeout, restConfig)
}

func NewScrapeClient(addr string, port int, path string, scheme string, timeout time.Duration, cfg *rest.Config) (ScrapeClient, error) {
	c := &http.Client{
		Timeout: timeout,
	}
	if cfg != nil && rest.IsConfigTransportTLS(*cfg) {
		transport, err := rest.TransportFor(cfg)
		if err != nil {
			return nil, err
		}
		c.Transport = transport
	}

	u := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(addr, strconv.Itoa(port)),
		Path:   path,
	}

	return &ScrapeClientImpl{
		httpClient: c,
		url:        u,
		buffers: &sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
	}, nil
}

func (s *ScrapeClientImpl) URL() string {
	return s.url.String()
}

func (s *ScrapeClientImpl) GetMetrics() ([]*model.Sample, error) {
	metricsURL := s.url.String()
	timeNow := time.Now()
	resp, err := s.httpClient.Get(metricsURL)
	metrics.RecordHttpClientQueryLatency(metricsURL, float64(time.Since(timeNow).Milliseconds()))
	if err != nil {
		metrics.RecordHttpClientQueryStatus(metricsURL, -1)
		return nil, err
	}
	defer func() {
		err1 := resp.Body.Close()
		if err1 != nil {
			klog.Warningf("close response body failed, err: %s", err1)
		}
	}()
	metrics.RecordHttpClientQueryStatus(metricsURL, resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request %s failed, code %d", metricsURL, resp.StatusCode)
	}

	b := s.getBuffer()
	defer s.returnBuffer(b)
	_, err = io.Copy(b, resp.Body)
	if err != nil {
		return nil, err
	}

	dec := expfmt.NewDecoder(b, expfmt.ResponseFormat(resp.Header))
	decoder := expfmt.SampleDecoder{
		Dec:  dec,
		Opts: &expfmt.DecodeOptions{},
	}

	var samples []*model.Sample
	for {
		var v model.Vector
		if err = decoder.Decode(&v); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		samples = append(samples, v...)
	}

	return samples, nil
}

func (s *ScrapeClientImpl) getBuffer() *bytes.Buffer {
	return s.buffers.Get().(*bytes.Buffer)
}

func (s *ScrapeClientImpl) returnBuffer(b *bytes.Buffer) {
	b.Reset()
	s.buffers.Put(b)
}
