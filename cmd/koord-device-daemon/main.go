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
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"
	"k8s.io/klog/v2"

	resourceconifg "github.com/koordinator-sh/koordinator/cmd/koord-device-daemon/config/v1"
	initconfig "github.com/koordinator-sh/koordinator/cmd/koord-device-daemon/init"
	printmanager "github.com/koordinator-sh/koordinator/pkg/device-daemon/manager"
	"github.com/koordinator-sh/koordinator/pkg/device-daemon/resource"
)

// Config represents a collection of config options for KDD.
type Config struct {
	configFile string
	// flags stores the CLI flags for later processing.
	flags []cli.Flag
}

func main() {
	klog.InitFlags(nil)
	config := &Config{}

	c := cli.NewApp()
	c.Name = "koord device daemon"
	c.Usage = "generate device infos  for heterogeneous devices"
	c.Action = func(ctx *cli.Context) error {
		return start(ctx, config)
	}

	c.Before = func(ctx *cli.Context) error {
		v := ctx.String("v")
		if err := flag.Set("v", v); err != nil {
			return fmt.Errorf("failed to set klog verbosity level: %w", err)
		}
		klog.V(2).InfoS("klog verbosity level set", "level", v)
		return nil
	}

	config.flags = []cli.Flag{
		&cli.StringFlag{
			Name:  "v",
			Value: "1",
			Usage: "klog verbosity level (e.g. 2 for Info, 5 for Debug)",
		},
		&cli.BoolFlag{
			Name:    "oneshot",
			Value:   false,
			Usage:   "Label once and exit",
			EnvVars: []string{"KDD_ONESHOT"},
		},
		&cli.BoolFlag{
			Name:    "no-timestamp",
			Value:   false,
			Usage:   "Do not add the timestamp to the labels",
			EnvVars: []string{"KDD_NO_TIMESTAMP"},
		},
		&cli.DurationFlag{
			Name:    "sleep-interval",
			Value:   900 * time.Second,
			Usage:   "Time to sleep between labeling",
			EnvVars: []string{"KDD_SLEEP_INTERVAL"},
		},
		&cli.StringFlag{
			Name:    "prints-output-file",
			Value:   "/var/run/koordlet/xpu-device-infos/ic-device",
			EnvVars: []string{"KDD_PRINTS_OUTPUT_FILE", "PRINTS_OUTPUT_FILE"},
		},
		&cli.StringFlag{
			Name:        "config-file",
			Usage:       "the path to a config file as an alternative to command line options or environment variables",
			Destination: &config.configFile,
			EnvVars:     []string{"KDD_CONFIG_FILE", "CONFIG_FILE"},
		},
	}

	c.Flags = config.flags

	if err := c.Run(os.Args); err != nil {
		klog.Error(err)
		os.Exit(1)
	}
}

// loadConfig loads the config from the spec file.
func (cfg *Config) loadConfig(c *cli.Context) (*resourceconifg.Config, error) {
	config, err := resourceconifg.NewConfig(c, cfg.flags)
	if err != nil {
		return nil, fmt.Errorf("unable to finalize config: %v", err)
	}

	return config, nil
}

func start(c *cli.Context, cfg *Config) error {
	defer func() {
		klog.Info("Exiting")
	}()

	klog.Info("Starting OS watcher.")
	sigs := signals(syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	for {
		// Load the configuration file
		klog.Info("Loading configuration.")
		config, err := cfg.loadConfig(c)
		if err != nil {
			return fmt.Errorf("unable to load config: %v", err)
		}

		// Print the config to the output.
		configJSON, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal config to JSON: %v", err)
		}
		klog.Infof("\nRunning with config:\n%v", string(configJSON))

		PrintsOutputer, err := printmanager.NewPrintsOutputer(config)
		if err != nil {
			return fmt.Errorf("failed to create prints outputer: %w", err)
		}

		klog.Info("Start running")
		d := &rfd{
			manager:        initconfig.ManagerMap,
			config:         config,
			PrintsOutputer: PrintsOutputer,
		}
		restart, err := d.run(sigs)
		if err != nil {
			return err
		}

		if !restart {
			return nil
		}
	}
}

type rfd struct {
	manager        map[string]resource.Manager
	config         *resourceconifg.Config
	PrintsOutputer printmanager.Outputer
}

func (d *rfd) run(sigs chan os.Signal) (bool, error) {
	defer func() {
		if d.config.Flags.KDD.Oneshot != nil && *d.config.Flags.KDD.Oneshot {
			return
		}
		if d.config.Flags.KDD.PrintsOutputFile != nil && *d.config.Flags.KDD.PrintsOutputFile == "" {
			return
		}

		err := removeOutputFile(*d.config.Flags.KDD.PrintsOutputFile)
		if err != nil {
			klog.Warningf("Error removing prints output file: %v", err)
		}
	}()

rerun:
	loopPrinters, err := printmanager.NewPrinter(d.manager, d.config)
	if err != nil {
		return false, nil
	}
	prints, err := loopPrinters.Prints()
	if err != nil {
		return false, fmt.Errorf("error generating prints: %v", err)
	}

	if len(prints) == 0 {
		klog.Warning("No prints generated from any source")
	}

	klog.Info("Creating Prints")

	if err := d.PrintsOutputer.OutputPrints(prints); err != nil {
		return false, fmt.Errorf("error printing: %v", err)
	}

	if *d.config.Flags.KDD.Oneshot {
		return false, nil
	}

	klog.Info("Sleeping for ", *d.config.Flags.KDD.SleepInterval)
	rerunTimeout := time.After(time.Duration(*d.config.Flags.KDD.SleepInterval))

	for {
		select {
		case <-rerunTimeout:
			goto rerun

		// Watch for any signals from the OS. On SIGHUP trigger a reload of the config.
		// On all other signals, exit the loop and exit the program.
		case s := <-sigs:
			switch s {
			case syscall.SIGHUP:
				klog.Info("Received SIGHUP, restarting.")
				return true, nil
			default:
				klog.Infof("Received signal %v, shutting down.", s)
				return false, nil
			}
		}
	}
}

func removeOutputFile(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to retrieve absolute path of output file: %v", err)
	}

	absDir := filepath.Dir(absPath)
	tmpDir := filepath.Join(absDir, "ic-device-tmp")

	err = os.RemoveAll(tmpDir)
	if err != nil {
		return fmt.Errorf("failed to remove temporary output directory: %v", err)
	}

	err = os.Remove(absPath)
	if err != nil {
		return fmt.Errorf("failed to remove output file: %v", err)
	}

	return nil
}

// signals creats a channel for the specified signals.
func signals(sigs ...os.Signal) chan os.Signal {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, sigs...)

	return sigChan
}
