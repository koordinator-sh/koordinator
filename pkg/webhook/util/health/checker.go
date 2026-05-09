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

package health

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sync"

	"github.com/fsnotify/fsnotify"
	"k8s.io/klog/v2"

	webhookutil "github.com/koordinator-sh/koordinator/pkg/webhook/util"
)

var (
	caCertFilePath = path.Join(webhookutil.GetCertDir(), "ca-cert.pem")

	onceWatch sync.Once
	lock      sync.Mutex
	client    *http.Client
	// newWatcher is a variable so tests can override fsnotify.NewWatcher.
	newWatcher = fsnotify.NewWatcher
	// watchCancel/watchDone allow tests to stop watcher goroutines safely.
	watchCancel context.CancelFunc
	watchDone   chan struct{}
)

func loadHTTPClientWithCACert() error {
	caCert, err := os.ReadFile(caCertFilePath)
	if err != nil {
		return err
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)
	lock.Lock()
	defer lock.Unlock()
	client = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		},
	}
	return nil
}

func watchCACert(ctx context.Context, watcher *fsnotify.Watcher) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			// Channel is closed.
			if !ok {
				return
			}

			// Only care about events which may modify the contents of the file.
			if !(isWrite(event) || isRemove(event) || isCreate(event)) {
				continue
			}

			klog.Infof("Watched ca-cert %v %v", event.Name, event.Op)

			// If the file was removed, re-add the watch.
			if isRemove(event) {
				if err := watcher.Add(event.Name); err != nil {
					klog.Errorf("Failed to re-watch ca-cert %v: %v", event.Name, err)
				}
			}

			if err := loadHTTPClientWithCACert(); err != nil {
				klog.Errorf("Failed to reload ca-cert %v: %v", event.Name, err)
			}

		case err, ok := <-watcher.Errors:
			// Channel is closed.
			if !ok {
				return
			}
			klog.Errorf("Failed to watch ca-cert: %v", err)
		}
	}
}

func isWrite(event fsnotify.Event) bool {
	return event.Op&fsnotify.Write == fsnotify.Write
}

func isCreate(event fsnotify.Event) bool {
	return event.Op&fsnotify.Create == fsnotify.Create
}

func isRemove(event fsnotify.Event) bool {
	return event.Op&fsnotify.Remove == fsnotify.Remove
}

func Checker(_ *http.Request) error {
	onceWatch.Do(func() {
		if err := loadHTTPClientWithCACert(); err != nil {
			klog.Errorf("Failed to load ca-cert for the first time: %v. Falling back to system defaults.", err)
			// Fall back to default HTTP client (system root CAs) to avoid panic.
			lock.Lock()
			client = http.DefaultClient
			lock.Unlock()
		}

		watcher, err := newWatcher()
		if err != nil {
			klog.Errorf("Failed to create ca-cert watcher: %v. Continuing without file watcher.", err)
			return
		}

		if err = watcher.Add(caCertFilePath); err != nil {
			klog.Errorf("Failed to watch ca-cert file %v: %v. Continuing without file watcher.", caCertFilePath, err)
			// Close watcher to avoid leaking OS resources before returning.
			_ = watcher.Close()
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		watchCancel = cancel
		watchDone = make(chan struct{})
		go func() {
			defer close(watchDone)
			defer watcher.Close()
			watchCACert(ctx, watcher)
		}()
	})

	url := fmt.Sprintf("https://localhost:%d/healthz", webhookutil.GetPort())
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")

	// If our shared client is the default fallback, attempt to reload CA once.
	lock.Lock()
	curClient := client
	lock.Unlock()

	if curClient == http.DefaultClient || curClient == nil {
		if err := loadHTTPClientWithCACert(); err != nil {
			klog.V(2).Infof("Retry loading ca-cert failed: %v", err)
		} else {
			lock.Lock()
			curClient = client
			lock.Unlock()
		}
	}

	if curClient == nil {
		curClient = http.DefaultClient
	}

	resp, err := curClient.Do(req)
	if resp != nil {
		// Ensure body is always closed to avoid leaking connections.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if err != nil {
		return err
	}
	return nil
}
