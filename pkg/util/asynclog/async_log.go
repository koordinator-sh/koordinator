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

package asynclog

import (
	"bytes"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
)

var (
	enableAsync = pflag.Bool("async-log", false, "Enable asynchronous logging to improve performance. When enabling async-log, should enable logtostderr and disable alsologtostderr at the same time. By default, klog outputs logs synchronously to stderr, which will affect performance when there are too many logs.")
	queueLength = pflag.Int("async-log-queue-length", 10000, "Control the log queue length to tune performance.")

	globalOutput *output
	once         sync.Once

	dataPool = &sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(nil)
		},
	}
)

func EnableAsyncIfNeed() bool {
	once.Do(func() {
		if *enableAsync {
			globalOutput = newOutput(*queueLength)
			klog.SetOutput(globalOutput)
			klog.LogToStderr(false)
		}
	})
	return *enableAsync
}

func FlushAndExit() {
	globalOutput.FlushAndExit()
}

type output struct {
	w            io.Writer
	logCh        chan *bytes.Buffer
	quit         chan bool
	shuttingDown int32
}

func newOutput(queueLength int) *output {
	o := &output{
		w:     os.Stderr,
		logCh: make(chan *bytes.Buffer, queueLength),
		quit:  make(chan bool),
	}
	go o.logger()
	return o
}

func (o *output) logger() {
	for {
		select {
		case <-o.quit:
		drain:
			for {
				select {
				case buff := <-o.logCh:
					buff.WriteTo(o.w)
				default:
					break drain
				}
			}
			close(o.quit)
			return
		case buff := <-o.logCh:
			buff.WriteTo(o.w)
			buff.Reset()
			dataPool.Put(buff)
		}
	}
}

func (o *output) FlushAndExit() {
	if atomic.CompareAndSwapInt32(&o.shuttingDown, 0, 1) {
		o.quit <- true
		<-o.quit
	}
}

func (o *output) Write(data []byte) (int, error) {
	if atomic.LoadInt32(&o.shuttingDown) == 1 {
		<-o.quit
		return o.w.Write(data)
	}
	// The data must be copied to a temporary buffer because the data may be reused
	buff := dataPool.Get().(*bytes.Buffer)
	buff.Write(data)
	o.logCh <- buff
	return len(data), nil
}
