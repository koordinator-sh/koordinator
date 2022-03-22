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

package main

import (
	"flag"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/cmd/slo-agent/options"
)

func main() {
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	// Default klog flush interval is 5 seconds, set to LogFlushFreq.
	go wait.Until(klog.Flush, *options.LogFlushFreq, wait.NeverStop)
	defer klog.Flush()

	go func() {
		klog.Info("starting prometheus server on %v", *options.PromAddr)
		http.Handle("/metrics", promhttp.Handler())
		klog.Fatalf("failed to start prometheus, error: %v", http.ListenAndServe(*options.PromAddr, nil))
	}()
}
