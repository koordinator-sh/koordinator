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
	"flag"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/transport"
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
	flag.StringVar(&token, "token", "mockTest", "")
	flag.Parse()

	server := httptest.NewTLSServer(http.HandlerFunc(mockPodsList))
	defer server.Close()

	address, portStr, err := parseHostAndPort(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	client, err := NewKubeletStub(address, port, 10, token)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := client.GetAllPods()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("podList %+v\n", ps)
}

func TestNewKubeletStub(t *testing.T) {
	type args struct {
		addr           string
		port           int
		timeoutSeconds int
		token          string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "127.0.0.1",
			args: args{
				addr:           "127.0.0.1",
				port:           10250,
				timeoutSeconds: 10,
				token:          "test_token",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewKubeletStub(tt.args.addr, tt.args.port, tt.args.timeoutSeconds, tt.args.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewKubeletStub() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.NotNil(t, got)
		})
	}
}

func Test_makeTransportConfig(t *testing.T) {
	inToken := "test_token"
	ts := &transport.Config{
		BearerToken: inToken,
		TLS: transport.TLSConfig{
			Insecure: true,
		},
	}
	type args struct {
		token    string
		insecure bool
	}
	tests := []struct {
		name string
		args args
		want *transport.Config
	}{
		{
			name: "transport",
			args: args{
				token: inToken,
			},
			want: ts,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := makeTransportConfig(tt.args.token, tt.args.insecure); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("makeTransportConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}
