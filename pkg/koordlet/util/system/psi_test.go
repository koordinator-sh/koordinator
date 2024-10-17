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

package system

import (
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	FullCorrectPSIContents = "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0"
)

func TestGetPSIByResource_CPUErr(t *testing.T) {
	dir := t.TempDir()
	psiPath := PSIPath{
		CPU: path.Join(dir, "cpu.pressure"),
		Mem: path.Join(dir, "memory.pressure"),
		IO:  path.Join(dir, "io.pressure"),
	}
	assert.NotPanics(t, func() {
		_, err := GetPSIByResource(psiPath)
		if err != nil {
			return
		}
	})
}

func TestGetPSIByResource_MemErr(t *testing.T) {
	helper := NewFileTestUtil(t)
	helper.CreateFile("cpu.pressure")
	helper.WriteFileContents("cpu.pressure", FullCorrectPSIContents)
	psiPath := PSIPath{
		CPU: path.Join(helper.TempDir, "cpu.pressure"),
		Mem: path.Join(helper.TempDir, "memory.pressure"),
		IO:  path.Join(helper.TempDir, "io.pressure"),
	}
	assert.NotPanics(t, func() {
		_, err := GetPSIByResource(psiPath)
		if err != nil {
			return
		}
	})
}

func TestGetPSIByResource_IOErr(t *testing.T) {
	helper := NewFileTestUtil(t)
	helper.CreateFile("cpu.pressure")
	helper.WriteFileContents("cpu.pressure", FullCorrectPSIContents)
	helper.CreateFile("memory.pressure")
	helper.WriteFileContents("memory.pressure", FullCorrectPSIContents)
	psiPath := PSIPath{
		CPU: path.Join(helper.TempDir, "cpu.pressure"),
		Mem: path.Join(helper.TempDir, "memory.pressure"),
		IO:  path.Join(helper.TempDir, "io.pressure"),
	}
	assert.NotPanics(t, func() {
		_, err := GetPSIByResource(psiPath)
		if err != nil {
			return
		}
	})
}

func TestGetPSIByResource(t *testing.T) {
	helper := NewFileTestUtil(t)
	helper.CreateFile("cpu.pressure")
	helper.WriteFileContents("cpu.pressure", FullCorrectPSIContents)
	helper.CreateFile("memory.pressure")
	helper.WriteFileContents("memory.pressure", FullCorrectPSIContents)
	helper.CreateFile("io.pressure")
	helper.WriteFileContents("io.pressure", FullCorrectPSIContents)
	psiPath := PSIPath{
		CPU: path.Join(helper.TempDir, "cpu.pressure"),
		Mem: path.Join(helper.TempDir, "memory.pressure"),
		IO:  path.Join(helper.TempDir, "io.pressure"),
	}
	assert.NotPanics(t, func() {
		_, err := GetPSIByResource(psiPath)
		if err != nil {
			return
		}
	})
}

func TestGetPSIRecords(t *testing.T) {
	helper := NewFileTestUtil(t)
	helper.CreateFile("cpu.pressure")
	helper.WriteFileContents("cpu.pressure", "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0")

	assert.NotPanics(t, func() {
		_, err := readPSI(helper.TempDir + "/cpu.pressure")
		if err != nil {
			return
		}
	})
}

func TestGetPSIRecords_wrongSomeFormat(t *testing.T) {
	helper := NewFileTestUtil(t)
	helper.CreateFile("cpu.pressure")
	helper.WriteFileContents("cpu.pressure", "some avg10=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0")

	_, err := readPSI(helper.TempDir + "/cpu.pressure")
	assert.NotNil(t, err)
}

func TestGetPSIRecords_wrongFullFormat(t *testing.T) {
	helper := NewFileTestUtil(t)
	helper.CreateFile("cpu.pressure")
	helper.WriteFileContents("cpu.pressure", "some avg10=0.00 total=0\nfull 0.00 avg300=0.00 total=0")

	_, err := readPSI(helper.TempDir + "/cpu.pressure")
	assert.NotNil(t, err)
}

func TestGetPSIRecords_wrongPrefix(t *testing.T) {
	helper := NewFileTestUtil(t)
	helper.CreateFile("cpu.pressure")
	helper.WriteFileContents("cpu.pressure", "wrong avg10=0.00 total=0\nfull 0.00 avg300=0.00 total=0")

	_, err := readPSI(helper.TempDir + "/cpu.pressure")
	assert.NotNil(t, err)
}

func TestGetPSIRecords_FullNotSupported(t *testing.T) {
	helper := NewFileTestUtil(t)
	helper.CreateFile("cpu.pressure")
	helper.WriteFileContents("cpu.pressure", "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")

	psi, err := readPSI(helper.TempDir + "/cpu.pressure")
	assert.Nil(t, err)
	assert.Equal(t, false, psi.FullSupported)
}
