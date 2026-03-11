/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-03-02 @author yangwanjin
 */

package app

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"

	"hybrid/cmd/hybrid-manager/app/options"
	"hybrid/pkg/controller"
	"hybrid/pkg/controller/classify"
)

func init() {
	// Register all controllers here. init() runs before main(), so they are
	// available when NewControllerManager builds the registry.
	runtime.Must(controller.Register(&classify.Controller{}))
	// runtime.Must(controller.Register(&resource.Controller{}))

}

// NewHybridManagerCommand creates the root cobra command for hybrid-manager.
func NewHybridManagerCommand() *cobra.Command {
	opts := options.NewHybridManagerOptions()

	cmd := &cobra.Command{
		Use:   "hybrid-manager",
		Short: "Sync AI prediction classifications and resources to Kubernetes workload annotations",
		Long: `hybrid-manager periodically downloads workload classification and resources results
from an AI prediction service and writes them as annotations onto the
corresponding Kubernetes workloads (Deployment, StatefulSet, DaemonSet, etc).`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			return Run(opts)
		},
	}

	// bind flags and klog flags
	opts.AddFlags(cmd.Flags())
	klog.InitFlags(nil)

	return cmd
}

// Run is the main entrypoint after flag parsing.
// It wires dependencies together and blocks until a shutdown signal is received.
func Run(s *options.HybridManagerOptions) error {
	s.MergeDefaultExcludeNamespaces()
	cm, err := s.NewControllerManager()
	if err != nil {
		return fmt.Errorf("failed to create controller manager: %w", err)
	}

	klog.Info("Starting hybrid-manager...")
	if err := cm.Run(controller.Controllers); err != nil {
		return fmt.Errorf("controller manager exited with error: %w", err)
	}

	klog.Info("hybrid-manager stopped")
	return nil
}
