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
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
)

func TestNewScrapeClientFromConfig(t *testing.T) {
	tests := []struct {
		name    string
		arg     *ClientConfig
		wantErr bool
		want    ScrapeClient
	}{
		{
			name: "new http client",
			arg: &ClientConfig{
				Timeout:     3 * time.Second,
				Address:     "127.0.0.1",
				Port:        9316,
				Path:        "/metrics",
				InsecureTLS: true,
			},
			wantErr: false,
			want: &ScrapeClientImpl{
				httpClient: &http.Client{
					Timeout: 3 * time.Second,
				},
				url: &url.URL{
					Scheme: "http",
					Host:   "127.0.0.1:9316",
					Path:   "/metrics",
				},
				buffers: &sync.Pool{
					New: func() interface{} {
						return new(bytes.Buffer)
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := NewScrapeClientFromConfig(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assertSameScrapeClientImpl(t, tt.want, got)
		})
	}
}

func TestScrapeClient(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		testRespBytes := `# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 50
# HELP go_threads Number of OS threads created.
# TYPE go_threads gauge
go_threads 30
# HELP process_cpu_seconds_total Total user and system CPU time spent in seconds.
# TYPE process_cpu_seconds_total counter
process_cpu_seconds_total 10000.00
`
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(testRespBytes))
		}))
		sURL, err := url.Parse(s.URL)
		assert.NoError(t, err)
		c := &ScrapeClientImpl{
			httpClient: &http.Client{
				Timeout: 3 * time.Second,
			},
			url: sURL,
			buffers: &sync.Pool{
				New: func() interface{} {
					return new(bytes.Buffer)
				},
			},
		}

		want := []*model.Sample{
			{
				Metric: model.Metric{
					model.MetricNameLabel: "go_goroutines",
				},
				Value: model.SampleValue(50),
			},
			{
				Metric: model.Metric{
					model.MetricNameLabel: "go_threads",
				},
				Value: model.SampleValue(30),
			},
			{
				Metric: model.Metric{
					model.MetricNameLabel: "process_cpu_seconds_total",
				},
				Value: model.SampleValue(10000),
			},
		}
		got, err := c.GetMetrics()
		assert.NoError(t, err)
		assertEqualSamplesOutOfOrder(t, want, got)

		s.Close()

		got, err = c.GetMetrics()
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}

func assertSameScrapeClientImpl(t *testing.T, want, got ScrapeClient) {
	if want == nil || got == nil {
		assert.Equal(t, want, got)
		return
	}
	ai, aOK := want.(*ScrapeClientImpl)
	bi, bOK := got.(*ScrapeClientImpl)
	assert.True(t, aOK)
	assert.True(t, bOK)
	aBufFn := ai.buffers
	bBufFn := bi.buffers
	assert.NotNil(t, aBufFn)
	assert.NotNil(t, bBufFn)
	ai.buffers = nil
	bi.buffers = nil
	assert.Equal(t, ai, bi)
	ai.buffers = aBufFn
	bi.buffers = bBufFn
}

func assertEqualSamplesOutOfOrder(t *testing.T, want, got []*model.Sample) {
	mW := map[model.LabelValue]*model.Sample{}
	for _, s := range want {
		mW[s.Metric[model.MetricNameLabel]] = s
	}
	mG := map[model.LabelValue]*model.Sample{}
	for _, s := range got {
		mG[s.Metric[model.MetricNameLabel]] = s
	}
	assert.Equal(t, len(mW), len(mG), "equal metric number")
	for k, s := range mW {
		sG, ok := mG[k]
		assert.True(t, ok, k)
		assert.Equal(t, s, sG)
	}
}
