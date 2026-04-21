/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-03-02 @author yangwanjin
 */

package kubernetes

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

// NewKubernetesClient returns a kubernetes.Interface built from kubeconfig.
// Returns an interface (not *Clientset) so callers can substitute fakes in tests.
func NewKubernetesClient(kubeconfig string) (kubernetes.Interface, error) {
	cfg, err := buildRestConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("build REST config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes clientset: %w", err)
	}
	return cs, nil
}

func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		klog.Info("Using in-cluster Kubernetes configuration")
		return cfg, nil
	}
	klog.InfoS("Not running in-cluster, falling back to kubeconfig", "path", kubeconfig)
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}
