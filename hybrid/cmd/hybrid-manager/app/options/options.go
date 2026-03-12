/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-03-02 @author yangwanjin
 */

package options

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"

	"hybrid/pkg/client"
	"hybrid/pkg/constants"
	"hybrid/pkg/controller"
	"hybrid/pkg/controller/options"
	"hybrid/pkg/predictor"
)

// HybridManagerOptions holds all configuration for hybrid-manager.
// Fields are populated from flags and environment variables.
type HybridManagerOptions struct {
	// Kubeconfig is the path to the kubeconfig file.
	// Empty string means in-cluster config.
	Kubeconfig string

	// SyncInterval controls how often classifications are synced to workload annotations.
	SyncInterval time.Duration

	// AIServer is the base URL of the AI prediction service.
	// Loaded from AI_SERVER env variable.
	AIServer string

	// AIToken is the auth token for the AI prediction service.
	// Loaded from AI_TOKEN env variable.
	AIToken string

	// ExcludeNamespaces is a list of namespaces whose workloads should never
	// have prediction annotations written to them.
	// Passed via --exclude-namespaces=kube-system,koordinator-system (comma-separated or repeated flags).
	ExcludeNamespaces []string

	// OutputDir is the directory where prediction CSV files are stored.
	OutputDir string
}

// NewHybridManagerOptions returns Options with default values applied.
func NewHybridManagerOptions() *HybridManagerOptions {
	opts := &HybridManagerOptions{
		SyncInterval: constants.DefaultSyncInterval,
		OutputDir:    constants.DefaultOutputDir,
	}

	if home := homedir.HomeDir(); home != "" {
		opts.Kubeconfig = filepath.Join(home, ".kube", "config")
	}

	return opts
}

// AddFlags binds flags to the option fields.
func (o *HybridManagerOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Kubeconfig, "kubeconfig", o.Kubeconfig,
		"Path to the kubeconfig file. Leave empty to use in-cluster config.")

	fs.DurationVar(&o.SyncInterval, "sync-interval", o.SyncInterval,
		"Interval for syncing predictions to workload annotations.")

	fs.StringArrayVar(&o.ExcludeNamespaces, "exclude-namespaces", o.ExcludeNamespaces,
		"Namespaces to skip when syncing annotations (e.g. --exclude-namespaces=kube-system --exclude-namespaces=koordinator-system).")

	fs.StringVar(&o.OutputDir, "output-dir", o.OutputDir,
		"Directory to store downloaded prediction CSV files.")
}

// Validate checks that all required configuration is present.
func (o *HybridManagerOptions) Validate() error {
	// Read secrets from environment (not flags, to avoid leaking in process list)
	o.AIServer = os.Getenv("AI_SERVER")
	o.AIToken = os.Getenv("AI_TOKEN")

	if o.AIServer == "" {
		return fmt.Errorf("environment variable AI_SERVER is required")
	}
	if o.AIToken == "" {
		return fmt.Errorf("environment variable AI_TOKEN is required")
	}
	if o.SyncInterval <= 0 {
		return fmt.Errorf("--sync-interval must be positive, got %v", o.SyncInterval)
	}

	return nil
}

func (o *HybridManagerOptions) MergeDefaultExcludeNamespaces() *HybridManagerOptions {
	o.ExcludeNamespaces = sets.List(
		sets.New[string](o.ExcludeNamespaces...).Insert(constants.DefaultExcludeNamespaces...))
	return o
}

// NewControllerManager wires all dependencies and returns a ready-to-run Manager.
func (o *HybridManagerOptions) NewControllerManager() (*controller.Manager, error) {
	k8sClient, err := client.NewKubernetesClient(o.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	downloadService := predictor.NewDownloadService(o.AIServer, o.AIToken, o.OutputDir)

	mgr := controller.NewManager(options.ManagerOptions{
		Client:            k8sClient,
		DownloadService:   downloadService,
		SyncInterval:      o.SyncInterval,
		ExcludeNamespaces: o.ExcludeNamespaces,
		OutputDir:         o.OutputDir,
		Context:           ctx,
		Cancel:            cancel,
	})

	klog.InfoS("Controller manager created", "syncInterval", o.SyncInterval, "outputDir", o.OutputDir)
	return mgr, nil
}
