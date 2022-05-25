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

package server

import (
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"k8s.io/klog/v2"

	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
)

type Options struct {
	Network string
	Address string
}

type Server interface {
	Setup() error
	Start() error
	Stop()
}

type server struct {
	listener net.Listener // socket our gRPC server listens on
	server   *grpc.Server // our gRPC server
	options  Options      // server options
	runtimeapi.UnimplementedRuntimeHookServiceServer
}

func (s *server) Setup() error {
	if err := s.createRPCServer(); err != nil {
		return err
	}
	runtimeapi.RegisterRuntimeHookServiceServer(s.server, s)
	return nil
}

func (s *server) Start() error {
	klog.Infof("starting runtime hook server on %s", s.options.Address)
	go func() {
		s.server.Serve(s.listener)
	}()
	return nil
}

func (s *server) Stop() {
	klog.Infof("stopping runtime hook server")
	s.server.Stop()
}

func (s *server) createRPCServer() error {
	if s.server != nil {
		return nil
	}
	l, err := net.Listen(s.options.Network, s.options.Address)
	if err != nil {
		return fmt.Errorf("failed to create runtime hook server, error: %w", err)
	}
	s.listener = l
	s.server = grpc.NewServer()
	reflection.Register(s.server)
	return nil
}

func NewServer(opt Options) (Server, error) {
	return &server{
		options: opt,
	}, nil
}
