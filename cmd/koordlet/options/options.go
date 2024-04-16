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

package options

import (
	"flag"
	"net/http"

	"k8s.io/klog/v2"
)

var (
	ServerAddr   = flag.String("addr", ":9316", "port of koordlet server")
	EnablePprof  = flag.Bool("enable-pprof", false, "Enable pprof for koordlet.")
	PprofAddr    = flag.String("pprof-addr", ":9317", "The address the pprof binds to.")
	KubeAPIQPS   = flag.Float64("kube-api-qps", 20.0, "QPS to use while talking with kube-apiserver.")
	KubeAPIBurst = flag.Int("kube-api-burst", 30, "Burst to use while talking with kube-apiserver.")
)

// ExtendedHTTPHandlerRegistry is the registry of extended HTTP handlers.
var ExtendedHTTPHandlerRegistry = map[string]func() http.HandlerFunc{}

func InstallExtendedHTTPHandler(mux *http.ServeMux) {
	for path, newHandler := range ExtendedHTTPHandlerRegistry {
		mux.HandleFunc(path, newHandler())
		klog.V(4).Infof("extended HTTP handler is registered on path %s", path)
	}
}
