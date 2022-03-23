//go:build !linux
// +build !linux

/*
Copyright 2021 Alibaba Cloud.

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

package pleg

import (
	"fmt"
	"runtime"

	"k8s.io/utils/inotify"
)

var errNotSupported = fmt.Errorf("watch not supported on %s", runtime.GOOS)

func NewWatcher() (Watcher, error) {
	return nil, errNotSupported
}

type notSupportedWatcher struct {
}

func (w *notSupportedWatcher) Close() error {
	return errNotSupported
}

func (w *notSupportedWatcher) AddWatch(path string) error {
	return errNotSupported
}

func (w *notSupportedWatcher) RemoveWatch(path string) error {
	return errNotSupported
}

func (w *notSupportedWatcher) Event() chan *inotify.Event {
	ch := make(chan *inotify.Event)
	close(ch)
	return ch
}

func (w *notSupportedWatcher) Error() chan error {
	return make(chan error)
}

func TypeOf(event *inotify.Event) EventType {
	if event.Mask&IN_CREATE != 0 && event.Mask&IN_ISDIR != 0 {
		return DirCreated
	}
	if event.Mask&IN_DELETE != 0 && event.Mask&IN_ISDIR != 0 {
		return DirRemoved
	}
	return UnknownType
}
