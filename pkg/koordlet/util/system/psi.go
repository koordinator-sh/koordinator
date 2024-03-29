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
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"k8s.io/klog/v2"
)

const psiLineFormat = "avg10=%f avg60=%f avg300=%f total=%d"

type PSIPath struct {
	CPU string
	Mem string
	IO  string
}

type PSIByResource struct {
	CPU PSIStats
	Mem PSIStats
	IO  PSIStats
}

type PSILine struct {
	Avg10  float64
	Avg60  float64
	Avg300 float64
	Total  uint64
}

type PSIStats struct {
	Some *PSILine
	Full *PSILine

	FullSupported bool
}

// parsePSIStats parses the specified file for pressure stall information.
func ParsePSIStats(r io.Reader) (PSIStats, error) {
	psiStats := PSIStats{}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		l := scanner.Text()
		prefix := strings.Split(l, " ")[0]
		switch prefix {
		case "some":
			psi := PSILine{}
			_, err := fmt.Sscanf(l, fmt.Sprintf("some %s", psiLineFormat), &psi.Avg10, &psi.Avg60, &psi.Avg300, &psi.Total)
			if err != nil {
				return PSIStats{}, err
			}
			psiStats.Some = &psi
		case "full":
			psi := PSILine{}
			_, err := fmt.Sscanf(l, fmt.Sprintf("full %s", psiLineFormat), &psi.Avg10, &psi.Avg60, &psi.Avg300, &psi.Total)
			if err != nil {
				return PSIStats{}, err
			}
			psiStats.Full = &psi
		default:
			return PSIStats{}, fmt.Errorf("unknown PSI prefix: %s", prefix)
		}
	}

	// full cpu pressure not supported in old kernel versions
	psiStats.FullSupported = true
	if psiStats.Full == nil {
		psiStats.FullSupported = false
		psiStats.Full = &PSILine{}
	}

	return psiStats, nil
}

func GetPSIByResource(paths PSIPath) (*PSIByResource, error) {
	cpuStats, err := readPSI(paths.CPU)
	if err != nil {
		return nil, err
	}
	memStats, err := readPSI(paths.Mem)
	if err != nil {
		return nil, err
	}
	ioStats, err := readPSI(paths.IO)
	if err != nil {
		return nil, err
	}
	return &PSIByResource{
		CPU: cpuStats,
		Mem: memStats,
		IO:  ioStats,
	}, nil
}

func readPSI(pressureFilePath string) (PSIStats, error) {
	fileContents, err := os.ReadFile(pressureFilePath)
	if err != nil {
		return PSIStats{}, err
	}
	klog.V(5).Infof("read psi file contents: %s, path is %v", string(fileContents), pressureFilePath)
	stats, err := ParsePSIStats(bytes.NewReader(fileContents))
	if err != nil {
		return PSIStats{}, err
	}
	return stats, nil
}
