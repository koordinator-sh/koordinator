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

package perf_group

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

func Test_InitBufferPool(t *testing.T) {
	eventsNum := map[int]struct{}{
		2: {},
	}
	InitBufferPool(eventsNum)
	assert.NotNil(t, BufPools[2])
}

func Test_NewPerfGroupCollector(t *testing.T) {
	assert.NotPanics(t, func() {
		LibInit()
	})
	InitBufferPool(map[int]struct{}{
		2: {},
	})
	// create fake os syscall and prepare fake event data
	tempDir := t.TempDir()
	fakeFds := make(map[int]*os.File)
	fakeCgroupFd, err := os.OpenFile(tempDir, os.O_RDONLY, os.ModeDir)
	assert.NoError(t, err)
	var perfIndexMutex, fakeIdMutex sync.Mutex
	var fakeId uint64
	// used to index the fake perf fd
	c1 := 0
	c2 := 2
	fakeSyscall := func(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno) {
		// according to the trap, return different fd and change a3 value
		if trap == unix.SYS_PERF_EVENT_OPEN {
			var fd *os.File
			perfIndexMutex.Lock()
			switch a3 {
			case 0:
				fd = fakeFds[c1]
				c1++
			case 1:
				fd = fakeFds[c2]
				c2++
			}
			perfIndexMutex.Unlock()
			return fd.Fd(), 0, 0
		}
		if trap == unix.SYS_IOCTL && a2 == uintptr(unix.PERF_EVENT_IOC_ID) {
			fakeIdMutex.Lock()
			switch a1 {
			case fakeFds[0].Fd():
				fakeId = 1
			case fakeFds[1].Fd():
				fakeId = 2
			case fakeFds[2].Fd():
				fakeId = 5
			case fakeFds[3].Fd():
				fakeId = 6
			}
			defer fakeIdMutex.Unlock()
			// used to mock the ioctl modify id
			*(*uint64)(unsafe.Pointer(a3)) = fakeId
		}
		return 0, 0, 0
	}
	j := 1
	perfFileName := t.TempDir()
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("%s/tmp_%d", perfFileName, i)
		fakeFds[i], err = os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0666)
		assert.NoError(t, err)
		// write header
		perfHeader := &perfValueHeader{
			Nr:          2,
			TimeEnabled: 10,
			TimeRunning: 10,
		}
		binary.Write(fakeFds[i], binary.LittleEndian, perfHeader)
		binary.Write(fakeFds[i], binary.LittleEndian, value{
			ID:    uint64(j),
			Value: 10,
		})
		j++
		binary.Write(fakeFds[i], binary.LittleEndian, value{
			ID:    uint64(j),
			Value: 10,
		})
		j++
		fakeFds[i].Seek(0, 0)
	}
	cpus := []int{0, 1}
	collector, err := NewPerfGroupCollector(fakeCgroupFd, cpus, []string{"cycles", "instructions"}, fakeSyscall)
	assert.NoError(t, err)
	res, err := GetContainerPerfResult(collector)
	assert.NoError(t, err)
	assert.Equal(t, float64(20), res["cycles"])
	assert.Equal(t, float64(20), res["instructions"])
	assert.NotPanics(t, func() {
		LibFinalize()
	})
}

func Test_GetContainerCyclesAndInstructions(t *testing.T) {
	// TODO fix nil here
	//LibInit()
	//InitBufferPool(map[int]struct{}{
	//	2: {},
	//})
	//tempDir := t.TempDir()
	//f, _ := os.OpenFile(tempDir, os.O_RDONLY, os.ModeDir)
	//collector, _ := NewPerfGroupCollector(f, []int{}, []string{"cycles", "instructions"}, syscall.Syscall6)
	//_, _, err := GetContainerCyclesAndInstructionsGroup(collector)
	//assert.Nil(t, err)
	//LibFinalize()
}
