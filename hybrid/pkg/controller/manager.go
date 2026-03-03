/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-03-02 @author yangwanjin
 */

package controller

import (
	"context"
	"fmt"
	"hybrid/pkg/controller/options"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"hybrid/pkg/predictor"
)

// Controller is the interface every controller must implement to participate in the Manager lifecycle.
type Controller interface {
	// Name returns the unique name of this controller (used as registry key and in --controllers flag matching).
	Name() string

	// Start initialises the controller and begins its reconcile loop.
	// It receives a fully-wired Manager so it can access shared clients and
	// services. Start must respect ctx cancellation and return when ctx is done.
	Start(ctx context.Context, mgr *Manager) error
}

// Hideable can be implemented by a Controller to exclude it from the --controllers
// help output (useful for internal or experimental controllers).
type Hideable interface {
	Hidden() bool
}

// Registry maps controller names to their singleton instances.
type Registry map[string]Controller

// Controllers is the global registry populated by init() calls in each
// controller sub-package.
var Controllers = make(Registry)

// Register adds a controller to the global registry.
// It returns an error if a controller with the same name is already registered,
// so callers can wrap it with runtime.Must() to catch misconfiguration at startup.
func Register(c Controller) error {
	if _, exists := Controllers[c.Name()]; exists {
		return fmt.Errorf("controller %q is already registered", c.Name())
	}
	Controllers[c.Name()] = c
	return nil
}

// Keys returns a sorted slice of controller names, skipping hidden controllers.
func (r Registry) Keys() []string {
	var keys []string
	for k, v := range r {
		if h, ok := v.(Hideable); ok && h.Hidden() {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Manager owns shared dependencies and drives the controller lifecycle.
// It intentionally does NOT embed controller-runtime Manager to keep the
// dependency surface minimal — hybrid-manager uses a simple polling model,
// not an event-driven reconcile loop.
type Manager struct {
	Client            kubernetes.Interface
	DownloadService   *predictor.Service
	SyncInterval      time.Duration
	ExcludeNamespaces sets.Set[string]
	OutputDir         string
	ctx               context.Context
	cancel            context.CancelFunc
}

// NewManager creates a Manager from the given options.
func NewManager(opts options.ManagerOptions) *Manager {
	return &Manager{
		Client:            opts.Client,
		DownloadService:   opts.DownloadService,
		SyncInterval:      opts.SyncInterval,
		ExcludeNamespaces: sets.New[string](opts.ExcludeNamespaces...),
		OutputDir:         opts.OutputDir,
		ctx:               opts.Context,
		cancel:            opts.Cancel,
	}
}

// Run starts all registered controllers and blocks until the context is cancelled.
// Controllers are started concurrently; a startup failure in any one controller
// cancels the whole manager (fail-fast at startup, matches k8s behaviour).
func (m *Manager) Run(registry Registry) error {
	defer m.cancel()

	errCh := make(chan error, len(registry))

	for name, ctr := range registry {
		klog.InfoS("Starting controller", "controller", name)
		go func(name string, ctr Controller) {
			if err := ctr.Start(m.ctx, m); err != nil {
				klog.ErrorS(err, "Controller exited with error", "controller", name)
				errCh <- fmt.Errorf("controller %q: %w", name, err)
			} else {
				errCh <- nil
			}
		}(name, ctr)
	}

	// Wait for all controllers to exit (they all stop when ctx is cancelled).
	var firstErr error
	for range registry {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
			// Cancel the context so remaining controllers also stop.
			m.cancel()
		}
	}

	return firstErr
}
