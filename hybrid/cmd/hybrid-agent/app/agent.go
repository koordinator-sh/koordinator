/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-04-09 @author yangwanjin
 */

package app

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"

	"hybrid/cmd/hybrid-agent/app/options"
	"hybrid/pkg/controller"
	"hybrid/pkg/controller/classify"
	"hybrid/pkg/controller/interference"
	"hybrid/pkg/controller/replicas"
)

func init() {
	runtime.Must(controller.Register(&classify.Controller{}))
	runtime.Must(controller.Register(&replicas.Controller{}))
	runtime.Must(controller.Register(&interference.Controller{}))
	// runtime.Must(controller.Register(&resource.Controller{}))
}

// NewHybridAgentCommand creates the root cobra command for hybrid-agent.
func NewHybridAgentCommand() *cobra.Command {
	opts := options.NewAgentOptions()

	cmd := &cobra.Command{
		Use:          "hybrid-agent",
		Short:        "Sync AI models result to kubernetes workload annotations",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			return Run(opts)
		},
	}

	opts.AddFlags(cmd.Flags())
	klog.InitFlags(nil)

	return cmd
}

// Run wires up and starts the agent, then blocks until the context is cancelled.
func Run(o *options.AgentOptions) error {

	o.MergeDefaultExcludeNamespaces()

	agent, err := o.NewAgent()
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	klog.Info("starting check AI server...")
	if err := agent.AlgoClient.WaitUntilReady(agent.Context); err != nil {
		// context 被取消(SIGTERM)时优雅退出
		if agent.Context.Err() != nil {
			klog.Info("Shutdown signal received during AI server health check, exiting")
			return nil
		}
		return fmt.Errorf("AI server health check failed: %w", err)
	}

	errCh := make(chan error, 2)

	klog.Info("starting collector goroutine")
	go func() {
		if err := agent.Collector.Start(); err != nil {
			errCh <- fmt.Errorf("failed to start collector: %w", err)
		} else {
			errCh <- nil
		}
	}()

	// Manager.Run already blocks and starts controllers internally,
	// so we call it directly in the main goroutine.
	klog.Info("starting manager")
	if err := agent.Manager.Run(controller.Controllers); err != nil {
		klog.ErrorS(err, "Manager exited with error")
		return err
	}

	// Wait for collector to finish (it will exit when context is cancelled)
	if err := <-errCh; err != nil {
		klog.ErrorS(err, "Collector exited with error")
		return err
	}

	klog.Info("hybrid-agent stopped")
	return nil
}
