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
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"

	"hybrid/pkg/controller"
	"hybrid/pkg/predictor"
)

var (
	kubeconfig string
	interval   time.Duration
)

func init() {
	if home := homedir.HomeDir(); home != "" {
		flag.StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.DurationVar(&interval, "interval", 5*time.Minute, "interval for syncing predictions to workload annotations")

	klog.InitFlags(nil)
}

func main() {

	flag.Parse()

	// read server and token from env variable
	server := os.Getenv("AI_SERVER")
	token := os.Getenv("AI_TOKEN")
	if server == "" || token == "" {
		klog.Errorf("AI_SERVER or AI_TOKEN env variable not set")
		return
	}

	config, err := buildConfig()
	if err != nil {
		klog.Fatalf("Failed to build kubernetes config: %v", err)
	}

	// create kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create kubernetes clientset: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start services
	var wg sync.WaitGroup
	downloadService := predictor.NewService(server, token)

	wg.Add(1)
	go func() {
		defer wg.Done()
		ctr := controller.NewClassifyController(clientset, interval, downloadService)
		if err := ctr.Start(ctx); err != nil {
			klog.Errorf("Failed to start classify controller, error: %v", err)
		}
	}()

	klog.Info("Successfully start hybrid manager")

	// wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	klog.Info("Shutting down...")
	cancel()
	wg.Wait()
	klog.Info("Hybrid manager stopped")
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
