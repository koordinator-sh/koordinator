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
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/pflag"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	_ "k8s.io/component-base/metrics/prometheus/clientgo" // load restclient and workqueue metrics
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/koordinator-sh/koordinator/cmd/koord-manager/extensions"
	"github.com/koordinator-sh/koordinator/cmd/koord-manager/options"
	extclient "github.com/koordinator-sh/koordinator/pkg/client"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	"github.com/koordinator-sh/koordinator/pkg/util/fieldindex"
	metricsutil "github.com/koordinator-sh/koordinator/pkg/util/metrics"
	kmmetrics "github.com/koordinator-sh/koordinator/pkg/util/metrics/koordmanager"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
	"github.com/koordinator-sh/koordinator/pkg/webhook"
	// +kubebuilder:scaffold:imports
)

var (
	setupLog = ctrl.Log.WithName("setup")

	restConfigQPS   = flag.Int("rest-config-qps", 30, "QPS of rest config.")
	restConfigBurst = flag.Int("rest-config-burst", 50, "Burst of rest config.")
)

func main() {
	var metricsAddr, pprofAddr string
	var healthProbeAddr string
	var enableLeaderElection, enablePprof bool
	var leaderElectionNamespace string
	var leaderElectResourceLock string
	var namespace string
	var syncPeriodStr string
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&healthProbeAddr, "health-probe-addr", ":8000", "The address the healthz/readyz endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", true, "Whether you need to enable leader election.")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", "koordinator-system",
		"This determines the namespace in which the leader election configmap will be created, it will use in-cluster namespace if empty.")
	flag.StringVar(&leaderElectResourceLock, "leader-elect-resource-lock", resourcelock.LeasesResourceLock,
		"The leader election resource lock for controller manager. e.g. 'leases', 'configmaps', 'endpoints', 'endpointsleases'")
	flag.StringVar(&namespace, "namespace", "",
		"Namespace if specified restricts the manager's cache to watch objects in the desired namespace. Defaults to all namespaces.")
	flag.BoolVar(&enablePprof, "enable-pprof", true, "Enable pprof for controller manager.")
	flag.StringVar(&pprofAddr, "pprof-addr", ":8090", "The address the pprof binds to.")
	flag.StringVar(&syncPeriodStr, "sync-period", "", "Determines the minimum frequency at which watched resources are reconciled.")
	opts := options.NewOptions()
	opts.InitFlags(flag.CommandLine)
	sloconfig.InitFlags(flag.CommandLine)
	utilfeature.DefaultMutableFeatureGate.AddFlag(pflag.CommandLine)
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
	rand.Seed(time.Now().UnixNano())
	ctrl.SetLogger(klogr.New())
	features.SetDefaultFeatureGates()

	if enablePprof {
		go func() {
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				setupLog.Error(err, "unable to start pprof")
			}
		}()
	}

	cfg := ctrl.GetConfigOrDie()
	setRestConfig(cfg)
	cfg.UserAgent = "koordinator-manager"

	setupLog.Info("new clientset registry")
	err := extclient.NewRegistry(cfg)
	if err != nil {
		setupLog.Error(err, "unable to init koordinator clientset and informer")
		os.Exit(1)
	}

	var syncPeriod *time.Duration
	if syncPeriodStr != "" {
		d, err := time.ParseDuration(syncPeriodStr)
		if err != nil {
			setupLog.Error(err, "invalid sync period flag")
		} else {
			syncPeriod = &d
		}
	}

	mgrOpt := ctrl.Options{
		Scheme:                     options.Scheme,
		Metrics:                    metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress:     healthProbeAddr,
		LeaderElection:             enableLeaderElection,
		LeaderElectionID:           "koordinator-manager",
		LeaderElectionNamespace:    leaderElectionNamespace,
		LeaderElectionResourceLock: leaderElectResourceLock,
		Cache:                      cache.Options{SyncPeriod: syncPeriod},
		NewClient:                  utilclient.NewClient,
	}

	if namespace != "" {
		mgrOpt.Cache.DefaultNamespaces = map[string]cache.Config{}
		mgrOpt.Cache.DefaultNamespaces[namespace] = cache.Config{}
	}

	installMetricsHandler(&mgrOpt)
	ctx := ctrl.SetupSignalHandler()

	if utilfeature.DefaultFeatureGate.Enabled(features.WebhookFramework) {
		setupLog.Info("setup webhook opt")
		webhook.SetupWithWebhookOpt(&mgrOpt)
	}

	mgr, err := ctrl.NewManager(cfg, mgrOpt)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	setupLog.Info("register field index")
	if err := fieldindex.RegisterFieldIndexes(mgr.GetCache()); err != nil {
		setupLog.Error(err, "failed to register field index")
		os.Exit(1)
	}

	if err := opts.ApplyTo(mgr); err != nil {
		setupLog.Error(err, "unable to setup controllers")
		os.Exit(1)
	}

	extensions.PrepareExtensions(cfg, mgr)
	// +kubebuilder:scaffold:builder

	if utilfeature.DefaultFeatureGate.Enabled(features.WebhookFramework) {
		setupLog.Info("setup webhook")
		if err = webhook.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to setup webhook")
			os.Exit(1)
		}
		setupLog.Info("initialize webhook")
		if err := webhook.Initialize(ctx, cfg); err != nil {
			setupLog.Error(err, "unable to initialize webhook")
			os.Exit(1)
		}
		if err := mgr.AddReadyzCheck("webhook-ready", webhook.Checker); err != nil {
			setupLog.Error(err, "unable to add readyz check")
			os.Exit(1)
		}
		go func() {
			setupLog.Info("wait webhook ready")
			if err = webhook.WaitReady(); err != nil {
				setupLog.Error(err, "unable to wait webhook ready")
				os.Exit(1)
			}
		}()
	} else {
		klog.V(4).Infof("webhook framework feature gate not enabled")
	}

	setupLog.Info("starting manager")
	extensions.StartExtensions(ctx, mgr)
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setRestConfig(c *rest.Config) {
	if *restConfigQPS > 0 {
		c.QPS = float32(*restConfigQPS)
	}
	if *restConfigBurst > 0 {
		c.Burst = *restConfigBurst
	}
}

func installMetricsHandler(mgr *ctrl.Options) {
	if mgr.Metrics.ExtraHandlers == nil {
		mgr.Metrics.ExtraHandlers = map[string]http.Handler{}
	}
	for path, handler := range map[string]http.Handler{
		kmmetrics.InternalHTTPPath: promhttp.HandlerFor(kmmetrics.InternalRegistry, promhttp.HandlerOpts{}),
		kmmetrics.ExternalHTTPPath: promhttp.HandlerFor(kmmetrics.ExternalRegistry, promhttp.HandlerOpts{}),
		kmmetrics.DefaultHTTPPath: promhttp.HandlerFor(
			metricsutil.MergedGatherFunc(kmmetrics.InternalRegistry, kmmetrics.ExternalRegistry, ctrlmetrics.Registry), promhttp.HandlerOpts{}),
	} {
		mgr.Metrics.ExtraHandlers[path] = handler
	}
}
