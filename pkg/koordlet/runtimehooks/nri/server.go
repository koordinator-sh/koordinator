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

package nri

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"go.uber.org/atomic"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

type nriConfig struct {
	Events []string `json:"events"`
}

// Stub is the interface of nri stub
//
//go:generate mockgen -source=server.go -destination=mock_nri.go -package=nri
type StubInterface interface {
	stub.Stub
}

type NriServer struct {
	cfg      nriConfig
	mask     stub.EventMask
	options  Options       // server options
	stubOpts []stub.Option // nri stub options

	mutex            sync.Mutex
	stopped          bool // if false, the stub will try to reconnect when stub.OnClose is invoked
	stub             StubInterface
	connID           int64
	connClosedSignal chan struct{}
}

// make it testable
var mockableNewNriStub = func(p interface{}, opts []stub.Option, onClose func()) (StubInterface, error) {
	return stub.New(p, append(opts, stub.WithOnClose(onClose))...)
}

const (
	events = "RunPodSandbox,RemovePodSandbox,CreateContainer,UpdateContainer"
)

var (
	_ = stub.ConfigureInterface(&NriServer{})
	_ = stub.SynchronizeInterface(&NriServer{})
	_ = stub.RunPodInterface(&NriServer{})
	_ = stub.RemovePodInterface(&NriServer{})
	_ = stub.CreateContainerInterface(&NriServer{})
	_ = stub.UpdateContainerInterface(&NriServer{})
)

func NewNriServer(opt Options) (*NriServer, error) {
	err := opt.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate nri server, err: %w", err)
	}
	stubOpts := []stub.Option{
		stub.WithPluginName(opt.NriPluginName),
		stub.WithPluginIdx(opt.NriPluginIdx),
		stub.WithSocketPath(filepath.Join(system.Conf.VarRunRootDir, opt.NriSocketPath)),
	}
	p := &NriServer{
		options:          opt,
		stubOpts:         stubOpts,
		connClosedSignal: make(chan struct{}, 10),
	}
	if p.mask, err = api.ParseEventMask(events); err != nil {
		klog.Errorf("failed to parse events %v", err)
		return p, err
	}
	p.cfg.Events = strings.Split(events, ",")
	return p, nil
}

func (p *NriServer) Start() (err error) {
	klog.V(4).Info("starting nri server")
	defer func() {
		klog.V(4).Infof("nri server started with err: %v", err)
	}()

	err = p.options.Validate()
	if err != nil {
		return err
	}
	// connect at first when start nri server
	if _, err := p.connect(); err != nil {
		return fmt.Errorf("failed to connect to nri servver: %v", err)
	}

	// then keep connection alive forever until stopped
	go p.keepAlive()
	return nil
}

func (p *NriServer) Stop() {
	if stopped := func() bool {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		ret := p.stopped
		p.stopped = true
		return ret
	}(); stopped {
		return
	}
	p.cleanConnection(-1)
}

func (p *NriServer) keepAlive() {
	// this action is important, we should not catch panic
	// so that we can find the problem
	defer func() {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		// close channel when no consumer
		close(p.connClosedSignal)
		p.connClosedSignal = nil
	}()
	for {
		_, ok := <-p.connClosedSignal
		if !ok {
			return
		}
		if stopped := func() bool {
			p.mutex.Lock()
			defer p.mutex.Unlock()
			return p.stopped
		}(); stopped {
			return
		}
		_ = wait.ExponentialBackoff(p.options.BackOff, p.connect)
		if p.connected() {
			continue
		}
		// still not connected ,retry it in next turn
		select {
		case p.connClosedSignal <- struct{}{}:
		default:
		}
	}
}

func (p *NriServer) connected() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.stub != nil
}

func (p *NriServer) connect() (bool, error) {
	if p.connected() {
		return true, nil
	}
	connID := time.Now().UnixNano()
	klog.Infof("connect to nri server, conn_id: %d", connID)
	stubIns, err := mockableNewNriStub(p, p.stubOpts, p.reconnect(connID))
	if err != nil {
		return false, err
	}
	err = p.tryDial(connID, stubIns, p.options.NriConnectTimeout)
	if err != nil {
		klog.Errorf("failed to connect to nri server, conn_id: %d, %v", connID, err)
		return false, err
	}
	p.mutex.Lock()
	p.stub = stubIns
	p.connID = connID
	p.mutex.Unlock()
	klog.Infof("connect to nri server successfully, conn_id: %d", connID)
	return true, nil
}

func (p *NriServer) tryDial(connID int64, stubIns stub.Stub, atLeastLiveTime time.Duration) error {
	success := time.After(atLeastLiveTime)
	errChan := make(chan error)
	returned := atomic.NewBool(false)
	go func() {
		err := stubIns.Run(context.Background())
		if err == nil {
			return
		}
		p.cleanConnection(connID)
		if !returned.Load() {
			// make sure the consumer exists, otherwise the goroutine will be blocked
			// and never exit, which will cause memory leak
			errChan <- err
		}
		close(errChan)
	}()
	select {
	case <-success:
		returned.Store(true)
		return nil
	case err := <-errChan:
		return err
	}
}

func (p *NriServer) reconnect(connID int64) func() {
	return func() {
		p.cleanConnection(connID)
	}
}

func (p *NriServer) cleanConnection(connID int64) {
	klog.Warningf("nri server connection closed, conn_id: %d", connID)
	defer klog.Infof("clean dead stub successfully, conn_id: %d", connID)
	p.mutex.Lock()
	if p.stub != nil && (p.connID == connID || connID == -1) {
		p.stub.Stop()
		// wait for the plugin to stop
		p.stub.Wait()
		p.stub = nil
		p.connID = -1
	}
	if p.connClosedSignal != nil {
		select {
		case p.connClosedSignal <- struct{}{}:
		default:
		}
	}
	p.mutex.Unlock()
}
