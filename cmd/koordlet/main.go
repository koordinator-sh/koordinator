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
	"google.golang.org/grpc"
	pb "k8s.io/kubelet/pkg/apis/podresources/v1alpha1"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/koordinator-sh/koordinator/cmd/koordlet/options"
	"github.com/koordinator-sh/koordinator/pkg/features"
	agent "github.com/koordinator-sh/koordinator/pkg/koordlet"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/config"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	metricsutil "github.com/koordinator-sh/koordinator/pkg/util/metrics"
)

const ServerPodResourcesKubeletSocket = "/pod-resources/koordlet.sock"

func main() {
	cfg := config.NewConfiguration()
	cfg.InitFlags(flag.CommandLine)
	klog.InitFlags(nil)
	flag.Parse()

	go wait.Forever(klog.Flush, 5*time.Second)
	defer klog.Flush()

	if *options.EnablePprof {
		go func() {
			klog.V(4).Infof("Starting pprof on %v", *options.PprofAddr)
			if err := http.ListenAndServe(*options.PprofAddr, nil); err != nil {
				klog.Errorf("Unable to start pprof on %v, error: %v", *options.PprofAddr, err)
			}
		}()
	}

	if err := features.DefaultMutableKoordletFeatureGate.SetFromMap(cfg.FeatureGates); err != nil {
		klog.Fatalf("Unable to setup feature-gates: %v", err)
	}

	stopCtx := signals.SetupSignalHandler()

	// setup the default auditor
	if features.DefaultKoordletFeatureGate.Enabled(features.AuditEvents) {
		audit.SetupDefaultAuditor(cfg.AuditConf, stopCtx.Done())
	}

	// Get a config to talk to the apiserver
	klog.Info("Setting up kubeconfig for koordlet")
	err := cfg.InitKubeConfigForKoordlet(*options.KubeAPIQPS, *options.KubeAPIBurst)
	if err != nil {
		klog.Fatalf("Unable to setup kubeconfig: %v", err)
	}

	d, err := agent.NewDaemon(cfg)
	if err != nil {
		klog.Fatalf("Unable to setup koordlet daemon: %v", err)
	}

	// Expose the Prometheus http endpoint
	go installHTTPHandler()

	// Start the Cmd
	klog.Info("Starting the koordlet daemon")
	d.Run(stopCtx.Done())
}

func installHTTPHandler() {
	klog.Infof("Starting prometheus server on %v", *options.ServerAddr)
	mux := http.NewServeMux()
	mux.Handle(metrics.ExternalHTTPPath, promhttp.HandlerFor(metrics.ExternalRegistry, promhttp.HandlerOpts{}))
	mux.Handle(metrics.InternalHTTPPath, promhttp.HandlerFor(metrics.InternalRegistry, promhttp.HandlerOpts{}))
	// merge internal and external
	mux.Handle(metrics.DefaultHTTPPath, promhttp.HandlerFor(
		metricsutil.MergedGatherFunc(metrics.InternalRegistry, metrics.ExternalRegistry), promhttp.HandlerOpts{}))
	if features.DefaultKoordletFeatureGate.Enabled(features.AuditEventsHTTPHandler) {
		mux.HandleFunc("/events", audit.HttpHandler())
	}
	// install extended HTTP handlers
	options.InstallExtendedHTTPHandler(mux)
	// http.HandleFunc("/healthz", d.HealthzHandler())
	klog.Fatalf("Prometheus monitoring failed: %v", http.ListenAndServe(*options.ServerAddr, mux))
}

type PodResourcesServer struct{}

var nodePodResources = []*pb.PodResources{
	{
		Name:      "pod-1",
		Namespace: "default",
		Containers: []*pb.ContainerResources{
			{
				Name: "container-1",
				Devices: []*pb.ContainerDevices{
					{
						ResourceName: "nvidia.com/gpu",
						DeviceIds:    []string{"GPU-32e51276-4ddd-5d40-b63a-7bf69ea08b2e", "GPU-6638b0e5-0708-3bef-74d1-5b75b85f6e75"},
					},
				},
			},
		},
	},
	{
		Name:      "pod-2",
		Namespace: "kube-system",
		Containers: []*pb.ContainerResources{
			{
				Name: "container-2",
				Devices: []*pb.ContainerDevices{
					{
						ResourceName: "nvidia.com/gpu",
						DeviceIds:    []string{"GPU-68d4072a-f4b8-46e5-c76f-66ce3d02cc38"},
					},
				},
			},
		},
	},
}

func (s *PodResourcesServer) List(ctx context.Context, req *pb.ListPodResourcesRequest) (*pb.ListPodResourcesResponse, error) {
	//var pods []*pb.PodResources
	klog.V(1).Infof("List(): start to list pod")

	return &pb.ListPodResourcesResponse{PodResources: nodePodResources}, nil
}

func startGrpc() error {
	lis, err := net.Listen("unix", ServerPodResourcesKubeletSocket)
	if err != nil {
		klog.Errorf("failed to listen: %v", err)
		return err
	}
	klog.V(1).Infof("setSocketPermissions...")
	if err := setSocketPermissions(ServerPodResourcesKubeletSocket); err != nil {
		klog.Errorf("failed to set socket permissions: %v", err)
		return err
	}
	klog.V(1).Infof("NewServer...")
	server := grpc.NewServer()
	klog.V(1).Infof("RegisterPodResourcesListerServer...")
	pb.RegisterPodResourcesListerServer(server, &PodResourcesServer{})

	klog.V(1).Infof("Starting gRPC server on %s", ServerPodResourcesKubeletSocket)
	if err := server.Serve(lis); err != nil {
		klog.Errorf("failed to serve: %v", err)
		return err
	}
	return nil
}

func setSocketPermissions(socketPath string) error {
	// In a real application, you would set the correct permissions here.
	// For example:
	return os.Chmod(socketPath, 0660)
	//return nil
}
