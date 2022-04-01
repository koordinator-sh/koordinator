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
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	minFreeKbytesFile = "/proc/sys/vm/min_free_kbytes"
)

type TestMetric struct {
	Time  time.Time
	Value int64
}

func printMetrics(metricsList interface{}) {
	metrics := reflect.ValueOf(metricsList)
	for i := 0; i < metrics.Len(); i++ {
		metricStruct := metrics.Index(i)
		fieldValue := metricStruct.FieldByName("Value")
		fieldTimeValue := metricStruct.FieldByName("Time")
		fmt.Printf("isTimeValid: %v\n", fieldTimeValue.IsValid())
		time, ok := fieldTimeValue.Interface().(time.Time)
		if !ok {
			fmt.Printf("time Type not ok!\n")
			continue
		}

		fmt.Printf("time:%v,value:%v\n", time, fieldValue)
	}
}

func Test_reflect(t *testing.T) {
	metrics := []TestMetric{
		{Value: 1},
		{Value: 2, Time: time.Now()},
	}
	printMetrics(metrics)
}

func Test_CommonFileWriteIfDifferent(t *testing.T) {

	type args struct {
		file     string
		oldValue string
		newValue string
	}

	tests := []struct {
		name      string
		args      args
		expectOld string
		expectNew string
	}{
		{
			name: "test_diffent_value",
			args: args{
				file:     minFreeKbytesFile,
				oldValue: "10000000",
				newValue: "20000000",
			},
			expectOld: "10000000",
			expectNew: "20000000",
		},
		{
			name: "test_equal_value",
			args: args{
				file:     minFreeKbytesFile,
				oldValue: "10000000",
				newValue: "10000000",
			},
			expectOld: "10000000",
			expectNew: "10000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.CreateFile(tt.args.file)

			CommonFileWriteIfDifferent(tt.args.file, tt.args.oldValue)

			gotOld, _ := CommonFileRead(tt.args.file)
			assert.Equal(t, tt.expectOld, gotOld)

			CommonFileWriteIfDifferent(tt.args.file, tt.args.newValue)
			gotNew, _ := CommonFileRead(tt.args.file)
			assert.Equal(t, tt.expectNew, gotNew)

		})
	}

}
