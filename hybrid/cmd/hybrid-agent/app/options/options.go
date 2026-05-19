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

	config "hybrid/config/collector"
	"hybrid/pkg/collector"
	"hybrid/pkg/constants"
	"hybrid/pkg/controller"
	"hybrid/pkg/controller/options"
	"hybrid/pkg/predictor"
	"hybrid/pkg/simple/algorithm"
	"hybrid/pkg/simple/kubernetes"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/homedir"
)

type AgentOptions struct {
	// Config is the path for config file. If empty, uses default locations.
	Config string

	// AIServer is the base URL of the AI prediction service.
	// Loaded from AI_SERVER env variable.
	AIServer string

	// AIToken is the auth token for the AI prediction service.
	// Loaded from AI_TOKEN env variable.
	AIToken string

	// Kubeconfig is the path to the kubeconfig file.
	// Empty string means in-cluster config.
	Kubeconfig string

	// ExcludeNamespaces is a list of namespaces whose workloads should never
	// have prediction annotations written to them.
	// Passed via --exclude-namespaces=kube-system,koordinator-system (comma-separated or repeated flags).
	ExcludeNamespaces []string

	// OutputDir is the directory where prediction CSV files are stored.
	OutputDir string

	// SaveModelResults controls whether raw model result JSON responses from the
	// algorithm service are persisted to disk under <OutputDir>/hybrid-results/.
	SaveModelResults bool

	// ModelResultsRetention is how long model result files are kept before
	// the cleanup goroutine deletes them.  Defaults to 24h.
	ModelResultsRetention time.Duration
}

func NewAgentOptions() *AgentOptions {
	opts := &AgentOptions{
		OutputDir:             constants.DefaultOutputDir,
		ModelResultsRetention: 24 * time.Hour,
	}

	if home := homedir.HomeDir(); home != "" {
		opts.Kubeconfig = filepath.Join(home, ".kube", "config")
	}

	return opts
}

func (o *AgentOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Config, "config", o.Config,
		"The path of the configuration file.")

	fs.StringVar(&o.Kubeconfig, "kubeconfig", o.Kubeconfig,
		"Path to the kubeconfig file. Leave empty to use in-cluster config.")

	fs.StringArrayVar(&o.ExcludeNamespaces, "exclude-namespaces", o.ExcludeNamespaces,
		"Namespaces to skip when syncing annotations (e.g. --exclude-namespaces=kube-system --exclude-namespaces=koordinator-system).")

	fs.StringVar(&o.OutputDir, "output-dir", o.OutputDir,
		"Directory to store downloaded prediction CSV files.")

	fs.BoolVar(&o.SaveModelResults, "save-model-results", o.SaveModelResults,
		"Persist raw model result JSON responses to <output-dir>/hybrid-results/ as gzip files.")

	fs.DurationVar(&o.ModelResultsRetention, "model-results-retention", o.ModelResultsRetention,
		"How long to keep model result files before they are deleted (e.g. 24h, 48h).")
}

func (o *AgentOptions) Validate() error {
	// Read secrets from environment (not flags, to avoid leaking in process list)
	o.AIServer = os.Getenv("AI_SERVER")
	o.AIToken = os.Getenv("AI_TOKEN")

	if o.AIServer == "" {
		return fmt.Errorf("environment variable AI_SERVER is required")
	}
	if o.AIToken == "" {
		return fmt.Errorf("environment variable AI_TOKEN is required")
	}

	return nil
}

func (o *AgentOptions) MergeDefaultExcludeNamespaces() *AgentOptions {
	o.ExcludeNamespaces = sets.List(
		sets.New[string](o.ExcludeNamespaces...).Insert(constants.DefaultExcludeNamespaces...))
	return o
}

type Agent struct {
	Context    context.Context
	AlgoClient *algorithm.Client
	Collector  *collector.Collector
	Manager    *controller.Manager
}

func (o *AgentOptions) NewAgent() (*Agent, error) {
	// load config for collector
	cfg, err := config.LoadConfig(o.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	k8sClient, err := kubernetes.NewKubernetesClient(o.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	algoClient := algorithm.NewClient(algorithm.WithURL(o.AIServer), algorithm.WithToken(o.AIToken))

	// Notifier: Watcher 写入模型运行任务通知消息, 控制器 Subscribe 负责读取
	notifier := algorithm.NewNotifier()

	// Watcher: 异步轮询任务状态, 成功后调用 notifier.notify
	watcher := algorithm.NewWatcher(algoClient, notifier)

	downloadService := predictor.NewDownloadService(algoClient, o.OutputDir)

	fetchService := predictor.NewFetchService(algoClient, o.OutputDir, o.SaveModelResults, o.ModelResultsRetention)

	hybridManager := controller.NewManager(options.ManagerOptions{
		Client:            k8sClient,
		DownloadService:   downloadService,
		FetchService:      fetchService,
		ExcludeNamespaces: o.ExcludeNamespaces,
		OutputDir:         o.OutputDir,
		Notifier:          notifier,
		Context:           ctx,
		Cancel:            cancel,
	})

	hybridCollector, err := collector.NewCollector(ctx, cfg, algoClient, watcher)
	if err != nil {
		return nil, fmt.Errorf("failed to build collector: %w", err)
	}

	agent := &Agent{
		Context:    ctx,
		AlgoClient: algoClient,
		Collector:  hybridCollector,
		Manager:    hybridManager,
	}

	return agent, nil
}
