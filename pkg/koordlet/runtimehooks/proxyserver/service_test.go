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

package proxyserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/apis/extension"
	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
)

func TestServer(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		// create and start server
		tmpDir := t.TempDir()
		opt := Options{
			Network:             "unix",
			Address:             filepath.Join(tmpDir, "koordlet.sock"),
			HostEndpoint:        filepath.Join(tmpDir, "koordlet.sock"),
			FailurePolicy:       "Ignore",
			PluginFailurePolicy: "Ignore",
			ConfigFilePath:      filepath.Join(tmpDir, "hookserver.d"),
			DisableStages:       map[string]struct{}{},
			Executor:            resourceexecutor.NewTestResourceExecutor(),
		}
		s, err := NewServer(opt)
		assert.NoError(t, err)
		assert.NotNil(t, s)
		stopCh := make(chan struct{})
		defer close(stopCh)
		opt.Executor.Run(stopCh)
		err = s.Setup()
		assert.NoError(t, err)
		err = s.Start()
		assert.NoError(t, err)
		defer s.Stop()

		ss, ok := s.(*server)
		assert.True(t, ok)
		assert.NotNil(t, ss)
		// PreRunPodSandboxHook
		podResp, err := ss.PreRunPodSandboxHook(context.TODO(), &runtimeapi.PodSandboxHookRequest{
			PodMeta: &runtimeapi.PodSandboxMetadata{
				Name:      "test-pod",
				Namespace: "test-ns",
				Uid:       "xxxxxx",
			},
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLS),
			},
			CgroupParent: "kubepods/pod-xxxxxx/",
		})
		assert.NoError(t, err)
		assert.NotNil(t, podResp)
		// PostStopPodSandboxHook
		podResp, err = ss.PostStopPodSandboxHook(context.TODO(), &runtimeapi.PodSandboxHookRequest{
			PodMeta: &runtimeapi.PodSandboxMetadata{
				Name:      "test-pod",
				Namespace: "test-ns",
				Uid:       "xxxxxx",
			},
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLS),
			},
			CgroupParent: "kubepods/pod-xxxxxx/",
		})
		assert.NoError(t, err)
		assert.NotNil(t, podResp)
		// PreCreateContainerHook
		containerResp, err := ss.PreCreateContainerHook(context.TODO(), &runtimeapi.ContainerResourceHookRequest{
			PodMeta: &runtimeapi.PodSandboxMetadata{
				Name:      "test-pod",
				Namespace: "test-ns",
				Uid:       "xxxxxx",
			},
			ContainerMeta: &runtimeapi.ContainerMetadata{
				Name: "test-container",
				Id:   "123",
			},
			PodLabels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLS),
			},
			PodCgroupParent: "kubepods/pod-xxxxxx/",
		})
		assert.NoError(t, err)
		assert.NotNil(t, containerResp)
		// PreStartContainerHook
		containerResp, err = ss.PreStartContainerHook(context.TODO(), &runtimeapi.ContainerResourceHookRequest{
			PodMeta: &runtimeapi.PodSandboxMetadata{
				Name:      "test-pod",
				Namespace: "test-ns",
				Uid:       "xxxxxx",
			},
			ContainerMeta: &runtimeapi.ContainerMetadata{
				Name: "test-container",
				Id:   "123",
			},
			PodLabels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLS),
			},
			PodCgroupParent: "kubepods/pod-xxxxxx/",
		})
		assert.NoError(t, err)
		assert.NotNil(t, containerResp)
		// PostStartContainerHook
		containerResp, err = ss.PostStartContainerHook(context.TODO(), &runtimeapi.ContainerResourceHookRequest{
			PodMeta: &runtimeapi.PodSandboxMetadata{
				Name:      "test-pod",
				Namespace: "test-ns",
				Uid:       "xxxxxx",
			},
			ContainerMeta: &runtimeapi.ContainerMetadata{
				Name: "test-container",
				Id:   "123",
			},
			PodLabels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLS),
			},
			PodCgroupParent: "kubepods/pod-xxxxxx/",
		})
		assert.NoError(t, err)
		assert.NotNil(t, containerResp)
		// PostStopContainerHook
		containerResp, err = ss.PostStopContainerHook(context.TODO(), &runtimeapi.ContainerResourceHookRequest{
			PodMeta: &runtimeapi.PodSandboxMetadata{
				Name:      "test-pod",
				Namespace: "test-ns",
				Uid:       "xxxxxx",
			},
			ContainerMeta: &runtimeapi.ContainerMetadata{
				Name: "test-container",
				Id:   "123",
			},
			PodLabels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLS),
			},
			PodCgroupParent: "kubepods/pod-xxxxxx/",
		})
		assert.NoError(t, err)
		assert.NotNil(t, containerResp)
		// PreUpdateContainerResourcesHook
		containerResp, err = ss.PreUpdateContainerResourcesHook(context.TODO(), &runtimeapi.ContainerResourceHookRequest{
			PodMeta: &runtimeapi.PodSandboxMetadata{
				Name:      "test-pod",
				Namespace: "test-ns",
				Uid:       "xxxxxx",
			},
			ContainerMeta: &runtimeapi.ContainerMetadata{
				Name: "test-container",
				Id:   "123",
			},
			PodLabels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLS),
			},
			PodCgroupParent: "kubepods/pod-xxxxxx/",
		})
		assert.NoError(t, err)
		assert.NotNil(t, containerResp)
	})
}
