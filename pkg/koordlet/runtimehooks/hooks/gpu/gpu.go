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

package gpu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/klog/v2"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

const GpuAllocEnv = "NVIDIA_VISIBLE_DEVICES"

const (
	// ldSoPreloadContainerPath is the system-wide preload file that glibc's
	// dynamic linker (ld.so) reads for every dynamically-linked process,
	// independent of the process environment.
	ldSoPreloadContainerPath = "/etc/ld.so.preload"
	// ldSoPreloadHostFileName is the file koordlet writes next to the HAMi-core
	// interception library and bind-mounts (read-only) as the container's
	// /etc/ld.so.preload.
	ldSoPreloadHostFileName = "ld.so.preload"
)

type gpuPlugin struct{}

func (p *gpuPlugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", "gpu env inject")
	hooks.Register(rmconfig.PreCreateContainer, "gpu env inject", "inject NVIDIA_VISIBLE_DEVICES env into container", p.InjectContainerGPUEnv)
}

var singleton *gpuPlugin

func Object() *gpuPlugin {
	if singleton == nil {
		singleton = &gpuPlugin{}
	}
	return singleton
}

func (p *gpuPlugin) InjectContainerGPUEnv(proto protocol.HooksProtocol) error {
	containerCtx := proto.(*protocol.ContainerContext)
	if containerCtx == nil {
		return fmt.Errorf("container protocol is nil for plugin gpu")
	}
	containerReq := containerCtx.Request
	alloc, err := ext.GetDeviceAllocations(containerReq.PodAnnotations)
	if err != nil {
		return err
	}
	devices, ok := alloc[schedulingv1alpha1.GPU]
	if !ok || len(devices) == 0 {
		klog.V(5).Infof("no gpu alloc info in pod anno, %s", containerReq.PodMeta.Name)
		return nil
	}
	gpuIDs := []string{}
	for _, d := range devices {
		gpuIDs = append(gpuIDs, fmt.Sprintf("%d", d.Minor))
	}
	if containerCtx.Response.AddContainerEnvs == nil {
		containerCtx.Response.AddContainerEnvs = make(map[string]string)
	}
	containerCtx.Response.AddContainerEnvs[GpuAllocEnv] = strings.Join(gpuIDs, ",")
	if containerReq.PodLabels[ext.LabelGPUIsolationProvider] == string(ext.GPUIsolationProviderHAMICore) {
		gpuResources := devices[0].Resources
		gpuMemoryRatio, ok := gpuResources[ext.ResourceGPUMemoryRatio]
		if !ok {
			return fmt.Errorf("gpu memory ratio not found in gpu resource")
		}
		if gpuMemoryRatio.Value() < 100 {
			gpuMemory, ok := gpuResources[ext.ResourceGPUMemory]
			if !ok {
				return fmt.Errorf("gpu memory not found in gpu resource")
			}
			containerCtx.Response.AddContainerEnvs["CUDA_DEVICE_MEMORY_LIMIT"] = fmt.Sprintf("%d", gpuMemory.Value())
			gpuCore, ok := gpuResources[ext.ResourceGPUCore]
			if ok {
				containerCtx.Response.AddContainerEnvs["CUDA_DEVICE_SM_LIMIT"] = fmt.Sprintf("%d", gpuCore.Value())
			}
			// Keep LD_PRELOAD for musl-based images, which honor the env var but
			// ignore /etc/ld.so.preload. On glibc the env var alone is NOT a
			// security boundary: the container's own entrypoint can run
			// `unset LD_PRELOAD` (or `env -u LD_PRELOAD`) before exec'ing the real
			// workload, and ld.so reads the variable from the process environment,
			// so the interception library is skipped for that exec.
			containerCtx.Response.AddContainerEnvs["LD_PRELOAD"] = system.Conf.HAMICoreLibraryDirectoryPath

			// Bind-mount a system-wide /etc/ld.so.preload read-only to make the
			// isolation non-bypassable from inside the container: ld.so reads that
			// file unconditionally for every dynamically-linked process, regardless
			// of the process environment, so the workload cannot opt out the way it
			// can with the env var.
			preloadHostPath, err := ensureHAMICorePreloadFile(system.Conf.HAMICoreLibraryDirectoryPath)
			if err != nil {
				// Fail closed: never leave the container with only the bypassable
				// env var, otherwise the tenant silently loses the GPU memory/compute
				// limit it was supposed to get.
				return fmt.Errorf("failed to prepare HAMi-core %s for GPU isolation: %w", ldSoPreloadContainerPath, err)
			}

			containerCtx.Response.AddContainerMounts = append(containerCtx.Response.AddContainerMounts,
				&protocol.Mount{
					Destination: ldSoPreloadContainerPath,
					Type:        "bind",
					Source:      preloadHostPath,
					Options:     []string{"rbind", "ro"},
				},
				&protocol.Mount{
					Destination: system.Conf.HAMICoreLibraryDirectoryPath,
					Type:        "bind",
					Source:      system.Conf.HAMICoreLibraryDirectoryPath,
					Options:     []string{"rbind"},
				},
				// Because https://github.com/Project-HAMi/HAMi/issues/696, we create the directory in pod.
				&protocol.Mount{
					Destination: "/tmp/vgpulock",
					Type:        "bind",
					Source:      "/tmp/vgpulock",
					Options:     []string{"rbind"},
				},
			)

			if features.DefaultKoordletFeatureGate.Enabled(features.HamiCoreVGPUMonitor) {
				hamiDirPath := filepath.Dir(system.Conf.HAMICoreLibraryDirectoryPath)
				containerCtx.Response.AddContainerEnvs["CUDA_DEVICE_MEMORY_SHARED_CACHE"] = fmt.Sprintf("%s/%s_%s.cache", hamiDirPath, containerReq.PodMeta.UID, containerReq.ContainerMeta.Name)
				cacheFileHostDirectory := fmt.Sprintf("%s/containers/%s_%s", hamiDirPath, containerReq.PodMeta.UID, containerReq.ContainerMeta.Name)
				// TODO: Move this operation into the pkg resource-executor.​
				klog.V(5).Infof("​​create a vgpu monitoring data directory [%s] and grant it 0777 permissions", cacheFileHostDirectory)
				os.RemoveAll(cacheFileHostDirectory)
				os.MkdirAll(cacheFileHostDirectory, 0777)
				os.Chmod(cacheFileHostDirectory, 0777)
				containerCtx.Response.AddContainerMounts = append(containerCtx.Response.AddContainerMounts,
					&protocol.Mount{
						Destination: hamiDirPath,
						Type:        "bind",
						Source:      cacheFileHostDirectory,
						Options:     []string{"rbind"},
					},
				)
			}
		}
	}

	return nil
}

// hamiCorePreloadHostPath returns the host path of the ld.so.preload file
// koordlet generates for HAMi-core isolation. It lives next to the interception
// library, which is already present on the host (it is the source of the
// libvgpu.so bind mount), so the directory is available to koordlet the same way
// the library is.
func hamiCorePreloadHostPath(libraryPath string) string {
	return filepath.Join(filepath.Dir(libraryPath), ldSoPreloadHostFileName)
}

// ensureHAMICorePreloadFile writes an ld.so.preload file on the host whose only
// entry is the container-internal path of the HAMi-core interception library, and
// returns that host path so the caller can bind-mount it as the container's
// /etc/ld.so.preload. The library path is identical inside the container because
// the library is bind-mounted at the exact same location. The write is
// idempotent: an existing file with the expected content is left untouched.
func ensureHAMICorePreloadFile(libraryPath string) (string, error) {
	hostPath := hamiCorePreloadHostPath(libraryPath)
	content := libraryPath + "\n"
	if old, err := os.ReadFile(hostPath); err == nil && string(old) == content {
		return hostPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(hostPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(hostPath, []byte(content), 0644); err != nil {
		return "", err
	}
	return hostPath, nil
}
