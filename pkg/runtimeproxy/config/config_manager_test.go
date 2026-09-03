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

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestConfigHelpers(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    FailurePolicyType
		wantErr bool
	}{
		{name: "fail", input: "Fail", want: PolicyFail},
		{name: "ignore", input: "Ignore", want: PolicyIgnore},
		{name: "unsupported", input: "Other", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetFailurePolicyType(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("GetFailurePolicyType(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetFailurePolicyType(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("GetFailurePolicyType(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}

	hookCases := []struct {
		name string
		hook RuntimeHookType
		path RuntimeRequestPath
		want bool
	}{
		{name: "pre run pod sandbox", hook: PreRunPodSandbox, path: RunPodSandbox, want: true},
		{name: "post stop pod sandbox", hook: PostStopPodSandbox, path: StopPodSandbox, want: true},
		{name: "pre create container", hook: PreCreateContainer, path: CreateContainer, want: true},
		{name: "pre start container", hook: PreStartContainer, path: StartContainer, want: true},
		{name: "post start container", hook: PostStartContainer, path: StartContainer, want: true},
		{name: "pre update container resources", hook: PreUpdateContainerResources, path: UpdateContainerResources, want: true},
		{name: "post stop container", hook: PostStopContainer, path: StopContainer, want: true},
		{name: "mismatch", hook: PreRunPodSandbox, path: StopContainer, want: false},
		{name: "unknown hook", hook: NoneRuntimeHookType, path: RunPodSandbox, want: false},
	}

	for _, tt := range hookCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.hook.OccursOn(tt.path); got != tt.want {
				t.Fatalf("%v.OccursOn(%v) = %v, want %v", tt.hook, tt.path, got, tt.want)
			}
		})
	}

	if got := RunPodSandbox.PreHookType(); got != PreRunPodSandbox {
		t.Fatalf("RunPodSandbox.PreHookType() = %v, want %v", got, PreRunPodSandbox)
	}
	if got := StopContainer.PreHookType(); got != NoneRuntimeHookType {
		t.Fatalf("StopContainer.PreHookType() = %v, want %v", got, NoneRuntimeHookType)
	}
	if got := RunPodSandbox.PostHookType(); got != NoneRuntimeHookType {
		t.Fatalf("RunPodSandbox.PostHookType() = %v, want %v", got, NoneRuntimeHookType)
	}
	if got := StopContainer.PostHookType(); got != NoneRuntimeHookType {
		t.Fatalf("StopContainer.PostHookType() = %v, want %v", got, NoneRuntimeHookType)
	}

	stageCases := []struct {
		name string
		hook RuntimeHookType
		want RuntimeHookStage
	}{
		{name: "pre stage", hook: PreCreateContainer, want: PreHook},
		{name: "post stage", hook: PostStartContainer, want: PostHook},
		{name: "unknown stage", hook: NoneRuntimeHookType, want: UnknownHook},
	}

	for _, tt := range stageCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.hook.HookStage(); got != tt.want {
				t.Fatalf("%v.HookStage() = %v, want %v", tt.hook, got, tt.want)
			}
		})
	}
}

func TestManagerUpdateAndReplaceConfig(t *testing.T) {
	m := NewConfigManager()
	m.watcher = mustWatcher(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "hook.json")
	writeConfigFile(t, configPath, RuntimeHookConfig{
		RemoteEndpoint: "unix:///tmp/runtime-hook.sock",
		FailurePolicy:  PolicyFail,
		RuntimeHooks:   []RuntimeHookType{PreRunPodSandbox},
	})

	if err := m.updateHookConfig(filepath.Join(dir, "ignore.txt")); err != nil {
		t.Fatalf("updateHookConfig(non-json) error = %v", err)
	}
	if got := len(m.configs); got != 0 {
		t.Fatalf("len(configs) after non-json update = %d, want 0", got)
	}

	if err := m.updateHookConfig(configPath); err != nil {
		t.Fatalf("updateHookConfig(json) error = %v", err)
	}
	item, ok := m.configs[configPath]
	if !ok {
		t.Fatalf("config %q was not registered", configPath)
	}
	if item.RuntimeHookConfig == nil {
		t.Fatal("config item was not populated")
	}
	if item.RemoteEndpoint != "unix:///tmp/runtime-hook.sock" {
		t.Fatalf("RemoteEndpoint = %q, want %q", item.RemoteEndpoint, "unix:///tmp/runtime-hook.sock")
	}
	if item.FailurePolicy != PolicyFail {
		t.Fatalf("FailurePolicy = %q, want %q", item.FailurePolicy, PolicyFail)
	}
	if got := len(item.RuntimeHooks); got != 1 || item.RuntimeHooks[0] != PreRunPodSandbox {
		t.Fatalf("RuntimeHooks = %v, want %v", item.RuntimeHooks, []RuntimeHookType{PreRunPodSandbox})
	}
	firstUpdate := item.updateTime

	if err := m.updateHookConfig(configPath); err != nil {
		t.Fatalf("second updateHookConfig(json) error = %v", err)
	}
	if got := m.configs[configPath].updateTime; !got.Equal(firstUpdate) {
		t.Fatalf("updateTime changed on refresh miss: got %v, want %v", got, firstUpdate)
	}

	replacedPath := filepath.Join(dir, "hook-replacement.json")
	writeConfigFile(t, replacedPath, RuntimeHookConfig{
		RemoteEndpoint: "unix:///tmp/runtime-hook-v2.sock",
		FailurePolicy:  PolicyIgnore,
		RuntimeHooks:   []RuntimeHookType{PostStartContainer},
	})
	if err := os.Rename(replacedPath, configPath); err != nil {
		t.Fatalf("rename replacement config: %v", err)
	}

	oldIno := item.fileIno
	if err := m.registerFileToWatchIfNeed(configPath); err != nil {
		t.Fatalf("registerFileToWatchIfNeed(replaced config) error = %v", err)
	}
	if got := m.configs[configPath].fileIno; got == oldIno {
		t.Fatalf("inode did not change after replacement: got %d", got)
	}
	if err := m.updateHookConfig(configPath); err != nil {
		t.Fatalf("reload replaced config error = %v", err)
	}
	if got := m.configs[configPath].RemoteEndpoint; got != "unix:///tmp/runtime-hook-v2.sock" {
		t.Fatalf("replacement reload = %q, want %q", got, "unix:///tmp/runtime-hook-v2.sock")
	}
}

func TestManagerNeedRefreshAndRemoval(t *testing.T) {
	m := NewConfigManager()
	m.watcher = mustWatcher(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "hook.json")
	writeConfigFile(t, configPath, RuntimeHookConfig{RemoteEndpoint: "unix:///tmp/runtime-hook.sock"})

	if err := m.updateHookConfig(configPath); err != nil {
		t.Fatalf("updateHookConfig error = %v", err)
	}
	if got := m.needRefreshConfig(configPath); got {
		t.Fatal("needRefreshConfig returned true for unchanged file")
	}

	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(configPath, future, future); err != nil {
		t.Fatalf("Chtimes config file: %v", err)
	}
	if got := m.needRefreshConfig(configPath); !got {
		t.Fatal("needRefreshConfig returned false for newer file")
	}
	if got := m.needRefreshConfig(filepath.Join(dir, "missing.json")); got {
		t.Fatal("needRefreshConfig returned true for missing file")
	}

	missingPath := filepath.Join(dir, "missing.json")
	m.removeFileToWatch(missingPath)

	if err := os.Remove(configPath); err != nil {
		t.Fatalf("remove config file: %v", err)
	}
	m.removeUnusedConfigs()
	if got := len(m.configs); got != 0 {
		t.Fatalf("len(configs) after removeUnusedConfigs = %d, want 0", got)
	}
}

func TestManagerCollectAllConfigs(t *testing.T) {
	oldPath := defaultRuntimeHookConfigPath
	defaultRuntimeHookConfigPath = t.TempDir()
	t.Cleanup(func() {
		defaultRuntimeHookConfigPath = oldPath
	})

	m := NewConfigManager()
	m.watcher = mustWatcher(t)

	validPath := filepath.Join(defaultRuntimeHookConfigPath, "valid.json")
	writeConfigFile(t, validPath, RuntimeHookConfig{
		RemoteEndpoint: "unix:///tmp/valid.sock",
		FailurePolicy:  PolicyIgnore,
		RuntimeHooks:   []RuntimeHookType{PreStartContainer, PostStopContainer},
	})
	if err := os.WriteFile(filepath.Join(defaultRuntimeHookConfigPath, "bad.json"), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write invalid json config: %v", err)
	}
	if err := os.Mkdir(filepath.Join(defaultRuntimeHookConfigPath, "subdir"), 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultRuntimeHookConfigPath, "notes.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatalf("write non-json file: %v", err)
	}

	if err := m.collectAllConfigs(); err != nil {
		t.Fatalf("collectAllConfigs error = %v", err)
	}
	if got := len(m.GetAllHook()); got != 2 {
		t.Fatalf("len(GetAllHook()) = %d, want 2", got)
	}
	if got := m.configs[validPath]; got == nil || got.RemoteEndpoint != "unix:///tmp/valid.sock" {
		t.Fatalf("valid config not loaded correctly: %#v", got)
	}
	badConfig := m.configs[filepath.Join(defaultRuntimeHookConfigPath, "bad.json")]
	if badConfig == nil {
		t.Fatal("invalid JSON config should still be registered")
	}
	if badConfig.RuntimeHookConfig != nil {
		t.Fatalf("invalid JSON config should not be populated: %#v", badConfig.RuntimeHookConfig)
	}
}

func TestManagerSyncLoopProcessesEvents(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "hook.json")
	writeConfigFile(t, configPath, RuntimeHookConfig{
		RemoteEndpoint: "unix:///tmp/runtime-hook.sock",
		FailurePolicy:  PolicyFail,
		RuntimeHooks:   []RuntimeHookType{PreRunPodSandbox},
	})

	stat, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	fileStat, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("failed to read inode for config file")
	}

	m := NewConfigManager()
	m.configs[configPath] = &RuntimeHookConfigItem{
		filePath:   configPath,
		fileIno:    fileStat.Ino,
		updateTime: time.Now().Add(-time.Minute),
		RuntimeHookConfig: &RuntimeHookConfig{
			RemoteEndpoint: "unix:///tmp/runtime-hook.sock",
			FailurePolicy:  PolicyFail,
			RuntimeHooks:   []RuntimeHookType{PreRunPodSandbox},
		},
	}
	m.watcher = &fsnotify.Watcher{
		Events: make(chan fsnotify.Event, 4),
		Errors: make(chan error, 1),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = m.syncLoop()
	}()
	t.Cleanup(func() {
		close(m.watcher.Events)
		close(m.watcher.Errors)
		<-done
	})

	initialItem, ok := getConfigItemSnapshot(m, configPath)
	if !ok {
		t.Fatalf("missing config item for %q", configPath)
	}
	initialUpdate := initialItem.updateTime
	m.watcher.Events <- fsnotify.Event{Name: configPath, Op: fsnotify.Chmod}
	waitForCondition(t, time.Second, func() bool {
		item, ok := getConfigItemSnapshot(m, configPath)
		return ok && item.updateTime.Equal(initialUpdate)
	})

	writeConfigFile(t, configPath, RuntimeHookConfig{
		RemoteEndpoint: "unix:///tmp/runtime-hook-updated.sock",
		FailurePolicy:  PolicyIgnore,
		RuntimeHooks:   []RuntimeHookType{PostStartContainer},
	})
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(configPath, future, future); err != nil {
		t.Fatalf("Chtimes updated config file: %v", err)
	}
	m.watcher.Events <- fsnotify.Event{Name: configPath, Op: fsnotify.Write}
	waitForCondition(t, time.Second, func() bool {
		item, ok := getConfigItemSnapshot(m, configPath)
		return ok && item.RemoteEndpoint == "unix:///tmp/runtime-hook-updated.sock" && item.FailurePolicy == PolicyIgnore
	})
}

func TestManagerRun(t *testing.T) {
	defaultRuntimeHookConfigPath = t.TempDir()

	m := NewConfigManager()
	if err := m.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if m.watcher == nil {
		t.Fatal("watcher was not initialized by Run()")
	}

	t.Cleanup(func() {
		if m.watcher != nil {
			_ = m.watcher.Close()
		}
	})

	if _, err := os.Stat(defaultRuntimeHookConfigPath); err != nil {
		t.Fatalf("runtime hook config path was not created: %v", err)
	}
}

func mustWatcher(t *testing.T) *fsnotify.Watcher {
	t.Helper()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("create watcher: %v", err)
	}
	t.Cleanup(func() {
		_ = watcher.Close()
	})
	return watcher
}

func writeConfigFile(t *testing.T, path string, config RuntimeHookConfig) {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config file %q: %v", path, err)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func getConfigItemSnapshot(m *Manager, path string) (RuntimeHookConfigItem, bool) {
	m.Lock()
	defer m.Unlock()
	item, ok := m.configs[path]
	if !ok || item == nil {
		return RuntimeHookConfigItem{}, false
	}
	snapshot := RuntimeHookConfigItem{
		filePath:   item.filePath,
		fileIno:    item.fileIno,
		updateTime: item.updateTime,
	}
	if item.RuntimeHookConfig != nil {
		hookCopy := *item.RuntimeHookConfig
		snapshot.RuntimeHookConfig = &hookCopy
	}
	return snapshot, true
}
