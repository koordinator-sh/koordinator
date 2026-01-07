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

import "github.com/containerd/nri/pkg/stub"

// Stub is the interface of nri stub
//
//go:generate mockgen -source=types.go -destination=mock_nri.go -package=nri
type StubInterface interface {
	stub.Stub
}

// make it testable
var newNriStub = func(p interface{}, opts []stub.Option, onClose func()) (StubInterface, error) {
	return stub.New(p, append(opts, stub.WithOnClose(onClose))...)
}

type nriConfig struct {
	Events []string `json:"events"`
}

type Server interface {
	stub.ConfigureInterface
	stub.SynchronizeInterface
	stub.RunPodInterface
	stub.RemovePodInterface
	stub.CreateContainerInterface
	stub.UpdateContainerInterface
	Start() (err error)
}
