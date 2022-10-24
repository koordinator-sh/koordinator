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
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
)

var (
	token string
)

func mockPodsList(w http.ResponseWriter, r *http.Request) {
	bear := r.Header.Get("Authorization")
	if bear == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(bear, "Bearer")
	if len(parts) != 2 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	http_token := strings.TrimSpace(parts[1])
	if len(http_token) < 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if http_token != token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	podList := new(corev1.PodList)
	b, err := json.Marshal(podList)
	if err != nil {
		log.Printf("codec error %+v", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func parseHostAndPort(rawURL string) (string, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "0", err
	}
	return net.SplitHostPort(u.Host)
}

func Test_kubeletStub_GetAllPods(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		token = "token"

		server := httptest.NewTLSServer(http.HandlerFunc(mockPodsList))
		defer server.Close()

		address, portStr, err := parseHostAndPort(server.URL)
		if err != nil {
			t.Fatal(err)
		}

		port, _ := strconv.Atoi(portStr)
		cfg := &rest.Config{
			Host:        net.JoinHostPort(address, portStr),
			BearerToken: token,
			TLSClientConfig: rest.TLSClientConfig{
				Insecure: true,
			},
		}

		client, err := NewKubeletStub(address, port, "https", 10*time.Second, cfg)
		if err != nil {
			t.Fatal(err)
		}
		ps, err := client.GetAllPods()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("podList %+v\n", ps)
	})
}

func TestNewKubeletStub(t *testing.T) {
	type args struct {
		addr    string
		port    int
		scheme  string
		timeout time.Duration
		config  *rest.Config
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "127.0.0.1",
			args: args{
				addr:    "127.0.0.1",
				port:    10250,
				scheme:  "https",
				timeout: 10 * time.Second,
				config: &rest.Config{
					BearerToken: token,
					TLSClientConfig: rest.TLSClientConfig{
						Insecure: true,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewKubeletStub(tt.args.addr, tt.args.port, tt.args.scheme, tt.args.timeout, tt.args.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewKubeletStub() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.NotNil(t, got)
		})
	}
}
