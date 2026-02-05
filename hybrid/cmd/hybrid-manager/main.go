/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-02-02 @author yangwanjin
 *
 */

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"

	"hybrid/pkg/controller"
)

var (
	kubeconfig     string
	predictionFile string
	syncInterval   time.Duration
)

func init() {
	if home := homedir.HomeDir(); home != "" {
		flag.StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.StringVar(&predictionFile, "prediction-file", "/data/prediction-result.csv", "path to the prediction result CSV file")
	flag.DurationVar(&syncInterval, "sync-interval", 5*time.Minute, "interval for syncing predictions to workload annotations")
	klog.InitFlags(nil)
}

func main() {

	flag.Parse()

	config, err := buildConfig()
	if err != nil {
		klog.Fatalf("Failed to build kubernetes config: %v", err)
	}

	// create kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create kubernetes clientset: %v", err)
	}

	classifyCtrl := controller.NewClassifyController(clientset, predictionFile, syncInterval)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// start controller
	go func() {
		if err := classifyCtrl.Start(ctx); err != nil {
			klog.Errorf("Controller error: %v", err)
			cancel()
		}
	}()

	klog.Info("Hybrid Predictor Manager started successfully")

	sig := <-sigCh
	klog.Infof("Received signal: %v, shutting down...", sig)

	classifyCtrl.Stop()
	cancel()

	time.Sleep(2 * time.Second)
	klog.Info("Hybrid Predictor Manager stopped")
}

func buildConfig() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Info("Not running in cluster, using kubeconfig")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	} else {
		klog.Info("Using in-cluster configuration")
	}

	return config, nil
}
