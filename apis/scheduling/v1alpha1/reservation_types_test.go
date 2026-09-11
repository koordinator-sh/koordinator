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

package v1alpha1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReservationStatusNominatedNodeName(t *testing.T) {
	// 1. Verify struct field exists and is string type
	statusType := reflect.TypeOf(ReservationStatus{})
	field, exists := statusType.FieldByName("NominatedNodeName")
	assert.True(t, exists, "ReservationStatus must have NominatedNodeName field")
	assert.Equal(t, reflect.String, field.Type.Kind(), "NominatedNodeName must be of string type")

	// 2. Verify JSON tag matches "nominatedNodeName,omitempty"
	jsonTag := field.Tag.Get("json")
	assert.Equal(t, "nominatedNodeName,omitempty", jsonTag)

	// 3. Verify Protobuf tag matches "bytes,7,opt,name=nominatedNodeName"
	protoTag := field.Tag.Get("protobuf")
	assert.Equal(t, "bytes,7,opt,name=nominatedNodeName", protoTag)

	// 4. Verify protobuf tag is unique by checking other fields' protobuf tags
	for i := 0; i < statusType.NumField(); i++ {
		f := statusType.Field(i)
		if f.Name == "NominatedNodeName" {
			continue
		}
		pTag := f.Tag.Get("protobuf")
		if pTag != "" {
			// Extract tag number (usually the second element in comma-separated parts)
			parts := strings.Split(pTag, ",")
			if len(parts) >= 2 {
				tagNumber := parts[1]
				assert.NotEqual(t, "7", tagNumber, "Field %s should not have protobuf tag 7", f.Name)
			}
		}
	}

	// 5. Test JSON Marshaling and Omitempty
	// When NominatedNodeName is empty, it should be omitted
	statusEmpty := ReservationStatus{}
	bytesEmpty, err := json.Marshal(statusEmpty)
	assert.NoError(t, err)
	assert.NotContains(t, string(bytesEmpty), "nominatedNodeName")

	// When NominatedNodeName is set, it should be present
	statusSet := ReservationStatus{
		NominatedNodeName: "node-xyz",
	}
	bytesSet, err := json.Marshal(statusSet)
	assert.NoError(t, err)
	assert.Contains(t, string(bytesSet), `"nominatedNodeName":"node-xyz"`)

	// Test Unmarshaling
	var statusUnmarshaled ReservationStatus
	err = json.Unmarshal(bytesSet, &statusUnmarshaled)
	assert.NoError(t, err)
	assert.Equal(t, "node-xyz", statusUnmarshaled.NominatedNodeName)

	// 6. Test DeepCopy correctness
	statusOrig := &ReservationStatus{
		NominatedNodeName: "node-original",
	}
	statusCopy := statusOrig.DeepCopy()
	assert.Equal(t, statusOrig.NominatedNodeName, statusCopy.NominatedNodeName)

	// Mutating the copy should not affect the original
	statusCopy.NominatedNodeName = "node-mutated"
	assert.Equal(t, "node-original", statusOrig.NominatedNodeName)
}
